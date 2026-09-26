package proxy

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"ai-unisub/internal/database"
)

const defaultFlushInterval = time.Minute

type proxyManager struct {
	mu            sync.RWMutex
	db            database.Database
	groups        map[int]*ProxyGroup
	flushInterval time.Duration
	stop          chan struct{}
	done          chan struct{}
	requests      sync.WaitGroup
}

func NewManager(db database.Database) ProxyManager {
	return &proxyManager{db: db, groups: make(map[int]*ProxyGroup), flushInterval: defaultFlushInterval}
}

func (m *proxyManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		select {
		case <-m.stop:
			return ErrManagerClosed
		default:
			return nil
		}
	}
	if m.db == nil {
		return errProxyDatabaseNil
	}
	records, err := m.db.ListProxyGroups()
	if err != nil {
		logger.ErrorAttrs("open_failed", slog.String("error", err.Error()))
		return err
	}
	groups := make(map[int]*ProxyGroup, len(records))
	for _, record := range records {
		group, err := decodeGroup(record)
		if err != nil {
			logger.ErrorAttrs("open_failed", slog.Int("group_id", record.ID), slog.String("error", err.Error()))
			return fmt.Errorf("decode proxy group %d: %w", record.ID, err)
		}
		groups[group.ID] = &group
	}
	m.groups = groups
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	go m.flushLoop(m.stop, m.done)
	logger.InfoAttrs("opened", slog.Int("groups", len(groups)))
	return nil
}

func (m *proxyManager) Close() error {
	m.mu.Lock()
	if m.stop == nil {
		m.mu.Unlock()
		return nil
	}
	select {
	case <-m.stop:
		m.mu.Unlock()
		return nil
	default:
	}
	stop, done := m.stop, m.done
	close(stop)
	m.mu.Unlock()

	<-done
	m.requests.Wait()
	if err := m.flushDirty(); err != nil {
		logger.ErrorAttrs("close_failed", slog.String("error", err.Error()))
		return err
	}
	logger.Info("closed", "")
	return nil
}

func (m *proxyManager) flushLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticks := time.Tick(m.flushInterval)
	for {
		select {
		case <-stop:
			return
		case <-ticks:
			if err := m.flushDirty(); err != nil {
				logger.ErrorAttrs("state_flush_failed", slog.String("error", err.Error()))
			}
		}
	}
}

func (m *proxyManager) List() []ProxyGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	groups := make([]ProxyGroup, 0, len(m.groups))
	for _, group := range m.groups {
		groups = append(groups, cloneGroup(*group))
	}
	slices.SortFunc(groups, func(a, b ProxyGroup) int { return cmp.Compare(a.ID, b.ID) })
	return groups
}

func (m *proxyManager) Create(config ProxyGroupConfig) (int, error) {
	config, err := validateConfig(config)
	if err != nil {
		return 0, err
	}
	group := ProxyGroup{Config: config, State: ProxyGroupState{Proxies: make(map[string]ProxyState)}}
	record, err := encodeGroup(group)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.lifecycleError(); err != nil {
		return 0, err
	}
	if err := m.db.SaveProxyGroup(&record); err != nil {
		return 0, err
	}
	if record.ID <= 0 {
		return 0, errDatabaseDidNotAssignProxyGroupID
	}
	group.ID = record.ID
	m.groups[group.ID] = &group
	logger.InfoAttrs("group_created", slog.Int("group_id", group.ID))
	return group.ID, nil
}

func (m *proxyManager) Delete(id int) error {
	if id <= 0 {
		return ErrProxyGroupNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.lifecycleError(); err != nil {
		return err
	}
	if _, ok := m.groups[id]; !ok {
		return ErrProxyGroupNotFound
	}
	if err := m.db.DeleteProxyGroup(id); err != nil {
		return err
	}
	delete(m.groups, id)
	logger.InfoAttrs("group_deleted", slog.Int("group_id", id))
	return nil
}

func (m *proxyManager) Update(id int, config ProxyGroupConfig) error {
	config, err := validateConfig(config)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.lifecycleError(); err != nil {
		return err
	}
	current, ok := m.groups[id]
	if !ok {
		return ErrProxyGroupNotFound
	}
	next := cloneGroup(*current)
	next.Config = config
	for address := range next.State.Proxies {
		if !slices.Contains(config.Proxies, address) {
			delete(next.State.Proxies, address)
		}
	}
	record, err := encodeGroup(next)
	if err != nil {
		return err
	}
	if err := m.db.SaveProxyGroup(&record); err != nil {
		return err
	}
	*current = next
	logger.InfoAttrs("group_updated", slog.Int("group_id", id))
	return nil
}

func (m *proxyManager) Do(id int, app string, req *http.Request, body []byte, handle func(res *http.Response) error) (*http.Response, error) {
	if req == nil {
		return nil, errRequestNil
	}
	if req.URL == nil {
		return nil, errRequestURLNil
	}
	m.mu.Lock()
	if err := m.lifecycleError(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	var group *ProxyGroup
	if id != 0 {
		stored, ok := m.groups[id]
		if !ok {
			m.mu.Unlock()
			return nil, ErrProxyGroupNotFound
		}
		group = new(cloneGroup(*stored))
	}
	m.requests.Add(1)
	m.mu.Unlock()
	defer m.requests.Done()

	if id == 0 {
		response, err := http.DefaultClient.Do(requestWithBody(req, body))
		if err != nil {
			logger.WarnAttrs("request_failed", slog.Int("group_id", id), slog.String("app", app))
			logErr := m.recordCall(0, "", app, req.URL.String(), 0, err.Error())
			return response, errors.Join(err, logErr)
		}
		handleErr := error(nil)
		if handle != nil {
			handleErr = handle(response)
		}
		message := ""
		if handleErr != nil {
			message = handleErr.Error()
			logger.WarnAttrs("response_rejected", slog.Int("group_id", id), slog.String("app", app), slog.Int("status", response.StatusCode))
		}
		logErr := m.recordCall(0, "", app, req.URL.String(), response.StatusCode, message)
		return response, errors.Join(handleErr, logErr)
	}
	addresses := orderedProxies(*group, app)
	if len(addresses) == 0 {
		logger.WarnAttrs("no_available_proxy", slog.Int("group_id", id), slog.String("app", app))
		return nil, ErrNoAvailableProxy
	}
	var attemptErrors []error
	for i, address := range addresses {
		attempt := requestWithBody(req, body)
		response, requestErr := doProxyRequest(attempt, address)
		if requestErr != nil {
			logger.WarnAttrs("proxy_attempt_failed", slog.Int("group_id", id), slog.String("app", app))
			m.setNetworkHealth(id, address, false)
			logErr := m.recordCall(id, address, app, req.URL.String(), 0, requestErr.Error())
			attemptErrors = append(attemptErrors, requestErr, logErr)
			continue
		}
		m.setNetworkHealth(id, address, true)
		handleErr := error(nil)
		if handle != nil {
			handleErr = handle(response)
		}
		if handleErr == nil {
			m.setApplicationHealth(id, address, app, true)
			logErr := m.recordCall(id, address, app, req.URL.String(), response.StatusCode, "")
			return response, logErr
		}
		m.setApplicationHealth(id, address, app, false)
		logger.WarnAttrs("proxy_response_rejected", slog.Int("group_id", id), slog.String("app", app), slog.Int("status", response.StatusCode))
		logErr := m.recordCall(id, address, app, req.URL.String(), response.StatusCode, handleErr.Error())
		attemptErrors = append(attemptErrors, handleErr, logErr)
		if i == len(addresses)-1 {
			return response, errors.Join(compactErrors(attemptErrors)...)
		}
		response.Body.Close()
	}
	return nil, errors.Join(compactErrors(attemptErrors)...)
}

func validateConfig(config ProxyGroupConfig) (ProxyGroupConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return ProxyGroupConfig{}, errProxyGroupNameRequired
	}
	if len(config.Proxies) == 0 {
		return ProxyGroupConfig{}, errNoProxies
	}
	config.Proxies = slices.Clone(config.Proxies)
	seen := make(map[string]struct{}, len(config.Proxies))
	for i, address := range config.Proxies {
		normalized, err := normalizeProxyURL(address)
		if err != nil {
			return ProxyGroupConfig{}, fmt.Errorf("proxy %d: %w", i+1, err)
		}
		if _, ok := seen[normalized]; ok {
			return ProxyGroupConfig{}, errDuplicateProxyURL
		}
		seen[normalized] = struct{}{}
		config.Proxies[i] = normalized
	}
	return config, nil
}

func normalizeProxyURL(address string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errInvalidProxyURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", errUnsupportedProxyURLScheme
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errProxyURLHasQueryOrFragment
	}
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String(), nil
}

func orderedProxies(group ProxyGroup, app string) []string {
	result := make([]string, 0, len(group.Config.Proxies))
	for _, healthy := range []bool{true, false} {
		for _, address := range group.Config.Proxies {
			if proxyHealthy(group.State, address, app) == healthy {
				result = append(result, address)
			}
		}
	}
	return result
}

func proxyHealthy(state ProxyGroupState, address, app string) bool {
	proxy, ok := state.Proxies[address]
	if !ok {
		return true
	}
	if !proxy.Healthy {
		return false
	}
	application, ok := proxy.Applications[app]
	return !ok || application.Healthy
}

func requestWithBody(req *http.Request, body []byte) *http.Request {
	attempt := req.Clone(req.Context())
	attempt.TransferEncoding = nil
	attempt.ContentLength = int64(len(body))
	if len(body) == 0 {
		attempt.Body = http.NoBody
		attempt.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
		return attempt
	}
	attempt.Body = io.NopCloser(bytes.NewReader(body))
	attempt.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return attempt
}

func doProxyRequest(req *http.Request, address string) (*http.Response, error) {
	proxyURL, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Transport: transport}
	response, err := client.Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	response.Body = &closeIdleBody{ReadCloser: response.Body, transport: transport}
	return response, nil
}

type closeIdleBody struct {
	io.ReadCloser
	transport *http.Transport
}

func (b *closeIdleBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	return err
}

func (m *proxyManager) setNetworkHealth(id int, address string, healthy bool) {
	m.updateState(id, func(state *ProxyGroupState) {
		proxy := state.Proxies[address]
		proxy.Healthy = healthy
		now := time.Now()
		if healthy {
			proxy.LastSuccess = now
		} else {
			proxy.LastFailure = now
		}
		state.Proxies[address] = proxy
	})
}

func (m *proxyManager) setApplicationHealth(id int, address, app string, healthy bool) {
	m.updateState(id, func(state *ProxyGroupState) {
		proxy := state.Proxies[address]
		if proxy.Applications == nil {
			proxy.Applications = make(map[string]ProxyApplicationState)
		}
		application := proxy.Applications[app]
		application.Healthy = healthy
		now := time.Now()
		if healthy {
			application.LastSuccess = now
		} else {
			application.LastFailure = now
		}
		proxy.Applications[app] = application
		state.Proxies[address] = proxy
	})
}

func (m *proxyManager) updateState(id int, update func(*ProxyGroupState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	group := m.groups[id]
	if group == nil {
		return
	}
	if group.State.Proxies == nil {
		group.State.Proxies = make(map[string]ProxyState)
	}
	update(&group.State)
	group.dirty = true
}

func (m *proxyManager) flushDirty() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var flushErrors []error
	for _, group := range m.groups {
		if !group.dirty {
			continue
		}
		record, err := encodeGroup(*group)
		if err == nil {
			err = m.db.SaveProxyGroup(&record)
		}
		if err != nil {
			flushErrors = append(flushErrors, fmt.Errorf("flush proxy group %d: %w", group.ID, err))
			continue
		}
		group.dirty = false
	}
	return errors.Join(flushErrors...)
}

func (m *proxyManager) lifecycleError() error {
	if m.stop == nil {
		return ErrManagerNotOpen
	}
	select {
	case <-m.stop:
		return ErrManagerClosed
	default:
		return nil
	}
}

func (m *proxyManager) recordCall(groupID int, proxyURL, app, requestURL string, status int, message string) error {
	if m.db == nil {
		return errProxyDatabaseNil
	}
	err := m.db.RecordProxyLog(&database.PersistedProxyLog{GroupID: groupID, ProxyURL: proxyURL, URL: requestURL, AppType: app, HTTPErrorCode: status, HTTPErrorMessage: message, Time: time.Now()})
	if err != nil {
		logger.ErrorAttrs("record_call_failed", slog.Int("group_id", groupID), slog.String("app", app), slog.String("error", err.Error()))
	}
	return err
}

func encodeGroup(group ProxyGroup) (database.PersistedProxyGroup, error) {
	config, err := json.Marshal(group.Config)
	if err != nil {
		return database.PersistedProxyGroup{}, err
	}
	state, err := json.Marshal(group.State)
	if err != nil {
		return database.PersistedProxyGroup{}, err
	}
	return database.PersistedProxyGroup{ID: group.ID, Config: config, State: state}, nil
}

func decodeGroup(record database.PersistedProxyGroup) (ProxyGroup, error) {
	group := ProxyGroup{ID: record.ID, State: ProxyGroupState{Proxies: make(map[string]ProxyState)}}
	if err := json.Unmarshal(record.Config, &group.Config); err != nil {
		return ProxyGroup{}, err
	}
	if len(record.State) > 0 && string(record.State) != "null" {
		if err := json.Unmarshal(record.State, &group.State); err != nil {
			return ProxyGroup{}, err
		}
	}
	if group.State.Proxies == nil {
		group.State.Proxies = make(map[string]ProxyState)
	}
	return group, nil
}

func cloneGroup(group ProxyGroup) ProxyGroup {
	clone := ProxyGroup{ID: group.ID, Config: ProxyGroupConfig{Name: group.Config.Name, Proxies: slices.Clone(group.Config.Proxies)}}
	clone.State.Proxies = make(map[string]ProxyState, len(group.State.Proxies))
	for address, state := range group.State.Proxies {
		state.Applications = cloneApplications(state.Applications)
		clone.State.Proxies[address] = state
	}
	return clone
}

func cloneApplications(source map[string]ProxyApplicationState) map[string]ProxyApplicationState {
	if source == nil {
		return nil
	}
	clone := make(map[string]ProxyApplicationState, len(source))
	for app, state := range source {
		clone[app] = state
	}
	return clone
}

func compactErrors(source []error) []error {
	result := source[:0]
	for _, err := range source {
		if err != nil {
			result = append(result, err)
		}
	}
	if len(result) == 0 {
		return []error{ErrNoAvailableProxy}
	}
	return result
}
