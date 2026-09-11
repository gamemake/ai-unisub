(function (global) {
  'use strict';
  var EMPTY = { me: null, providers: [], keys: [], users: [], recentCalls: [], logs: [], usageBySubscription: { data: [], totals: {} }, usageByUser: { data: [], totals: {} } };

  function clone(value) { return value == null ? value : JSON.parse(JSON.stringify(value)); }

  function createHomeDataStore(options) {
    options = options || {};
    var records = {}, listeners = {}, requests = {}, requestIDs = {};
    Object.keys(EMPTY).forEach(function (key) {
      records[key] = { key: key, data: clone(EMPTY[key]), status: 'idle', error: null, isPlaceholder: true, updatedAt: null, version: 0, requestId: 0 };
      listeners[key] = []; requestIDs[key] = 0;
    });
    function snapshot(key) {
      if (!records[key]) throw new Error('Unknown home data key: ' + key);
      var item = records[key];
      return { key: key, data: clone(item.data), status: item.status, error: item.error, isPlaceholder: item.isPlaceholder, updatedAt: item.updatedAt, version: item.version, requestId: item.requestId };
    }
    function notify(key) { var current = snapshot(key); listeners[key].slice().forEach(function (listener) { try { listener(current); } catch (error) { setTimeout(function () { throw error; }, 0); } }); }
    function setRecord(key, changes) { Object.keys(changes).forEach(function (field) { records[key][field] = changes[field]; }); notify(key); }
    function load(key, force) {
      if (!records[key]) return Promise.reject(new Error('Unknown home data key: ' + key));
      if (requests[key] && !force) return requests[key];
      if (typeof options.load !== 'function') return Promise.reject(new Error('HomeDataStore loader is not configured'));
      var id = ++requestIDs[key], oldData = records[key].data, hasData = Array.isArray(oldData) ? oldData.length > 0 : oldData != null;
      setRecord(key, { status: hasData ? 'refreshing' : 'loading', error: null, requestId: id });
      var promise = Promise.resolve().then(function () { return options.load(key, options.request); }).then(function (data) {
        if (id !== requestIDs[key]) return data;
        setRecord(key, { data: data == null ? clone(EMPTY[key]) : data, status: 'ready', error: null, isPlaceholder: false, updatedAt: new Date().toISOString(), version: records[key].version + 1 });
        return data;
      }).catch(function (error) {
        if (id !== requestIDs[key]) throw error;
        setRecord(key, { status: 'error', error: error }); throw error;
      }).finally(function () { if (requests[key] === promise) requests[key] = null; });
      requests[key] = promise; return promise;
    }
    return {
      get: snapshot,
      subscribe: function (key, listener) { if (!records[key]) throw new Error('Unknown home data key: ' + key); listeners[key].push(listener); listener(snapshot(key)); return function () { listeners[key] = listeners[key].filter(function (item) { return item !== listener; }); }; },
      load: load,
      refresh: function (key) { return load(key, true); },
      mutate: function (key, updater) { setRecord(key, { data: updater(clone(records[key].data)), status: 'ready', error: null, isPlaceholder: false, updatedAt: new Date().toISOString(), version: records[key].version + 1 }); },
      reset: function (key) { setRecord(key, { data: clone(EMPTY[key]), status: 'idle', error: null, isPlaceholder: true, updatedAt: null }); },
      destroy: function () { Object.keys(listeners).forEach(function (key) { listeners[key] = []; }); Object.keys(requests).forEach(function (key) { requests[key] = null; }); }
    };
  }
  global.createHomeDataStore = createHomeDataStore;
})(window);
