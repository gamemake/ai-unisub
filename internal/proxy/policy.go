package proxy

import (
	"cmp"
	"math"
	"math/rand/v2"
	"time"
)

func normalizePolicy(p Policy) Policy {
	p.NetworkFailureThreshold = cmp.Or(p.NetworkFailureThreshold, p.FailureThreshold)
	p.ApplicationFailureThreshold = cmp.Or(p.ApplicationFailureThreshold, p.FailureThreshold)
	if p.NetworkFailureThreshold < 1 || p.ApplicationFailureThreshold < 1 {
		panic("invalid failure threshold")
	}
	if p.Window == 0 {
		p.Window = 5 * time.Minute
	}
	p.MinSamples = cmp.Or(p.MinSamples, 5)
	p.ApplicationCooldown = cmp.Or(p.ApplicationCooldown, p.Cooldown)
	p.NetworkRecoverySuccesses = cmp.Or(p.NetworkRecoverySuccesses, 1)
	p.ApplicationRecoverySuccesses = cmp.Or(p.ApplicationRecoverySuccesses, 1)
	if p.MaxProbeInterval == 0 {
		p.MaxProbeInterval = 10 * time.Minute
	}
	p.ProbeConcurrency = cmp.Or(p.ProbeConcurrency, 4)
	if p.Window < time.Minute || p.Window > 10*time.Minute || p.Window%time.Minute != 0 || p.MinSamples < 1 || math.IsNaN(p.NetworkFailureRate) || math.IsNaN(p.ApplicationFailureRate) || p.NetworkFailureRate < 0 || p.NetworkFailureRate > 1 || p.ApplicationFailureRate < 0 || p.ApplicationFailureRate > 1 || p.ApplicationCooldown <= 0 || p.NetworkRecoverySuccesses < 1 || p.ApplicationRecoverySuccesses < 1 || p.MaxProbeInterval < p.Cooldown || p.ProbeConcurrency < 1 {
		panic("invalid proxy health policy")
	}
	return p
}

func (m *Manager) failureRateExceeded(k stateKey, now time.Time) bool {
	threshold := m.policy.NetworkFailureRate
	if k.app != "" {
		threshold = m.policy.ApplicationFailureRate
	}
	if threshold == 0 {
		return false
	}
	from := now.UTC().Truncate(time.Minute).Add(-m.policy.Window + time.Minute)
	var requests, failures int64
	for key, b := range m.minutes {
		if key.stateKey == k && !key.at.Before(from) && !key.at.After(now) {
			requests += b.Requests
			failures += b.Failures
		}
	}
	return requests >= m.policy.MinSamples && float64(failures)/float64(requests) >= threshold
}

func (m *Manager) markFailed(k stateKey, now time.Time, source string, force bool) {
	s := m.state(k.address, k.app)
	before := *s
	reason := "failure_rate"
	defer func() { logState(k, before, s, source, reason) }()
	s.ConsecutiveFailures++
	s.RecoverySuccesses = 0
	s.LastFailure = now
	threshold := m.policy.NetworkFailureThreshold
	s.LastError = NetworkError
	if k.app != "" {
		threshold = m.policy.ApplicationFailureThreshold
		s.LastError = ApplicationError
	}
	if !force && s.ConsecutiveFailures < threshold && s.Status != "half_open" && s.Status != "unavailable" && !m.failureRateExceeded(k, now) {
		return
	}
	switch {
	case force:
		reason = "probe_failed"
	case s.Status == "half_open":
		reason = "half_open_failed"
	case s.Status == "unavailable":
		reason = "continued_failure"
	case s.ConsecutiveFailures >= threshold:
		reason = "consecutive_failures"
	}
	s.Status = "unavailable"
	delay := m.policy.ApplicationCooldown
	if k.app == "" {
		delay = m.policy.Cooldown
		for i := 0; i < min(s.Backoff, 20) && delay < m.policy.MaxProbeInterval; i++ {
			delay = min(delay*2, m.policy.MaxProbeInterval)
		}
		// Bounded jitter never exceeds the configured cap.
		delay = time.Duration(float64(delay) * (.9 + rand.Float64()*.1))
		s.Backoff++
	}
	s.CooldownUntil = now.Add(delay)
}

func (m *Manager) markSucceeded(k stateKey, now time.Time, source string) {
	s := m.state(k.address, k.app)
	before := *s
	reason := "success"
	if s.Status == "half_open" || s.Status == "unavailable" {
		reason = "recovery_threshold"
	}
	defer func() { logState(k, before, s, source, reason) }()
	s.LastSuccess = now
	if s.Status == "half_open" || s.Status == "unavailable" {
		s.RecoverySuccesses++
		required := m.policy.NetworkRecoverySuccesses
		if k.app != "" {
			required = m.policy.ApplicationRecoverySuccesses
		}
		if s.RecoverySuccesses < required {
			reason = "recovery_pending"
			s.Status = "half_open"
			return
		}
	}
	s.Status = "available"
	s.ConsecutiveFailures, s.RecoverySuccesses, s.Backoff = 0, 0, 0
	s.CooldownUntil = time.Time{}
}
