package main

import (
	"sync"
)

// mockClient implements RemoteClient for testing.
type mockClient struct {
	mu         sync.Mutex
	connected  bool
	uploads    []mockUpload
	mkdirs     []string
	connectErr error
	uploadErr  error
	mkdirErr   error
	closeErr   error
}

type mockUpload struct {
	Local  string
	Remote string
}

func (m *mockClient) Connect() error {
	if m.connectErr != nil {
		return m.connectErr
	}
	m.mu.Lock()
	m.connected = true
	m.mu.Unlock()
	return nil
}

func (m *mockClient) Close() error {
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()
	if m.closeErr != nil {
		return m.closeErr
	}
	return nil
}

func (m *mockClient) Upload(local, remote string) error {
	if m.uploadErr != nil {
		return m.uploadErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploads = append(m.uploads, mockUpload{Local: local, Remote: remote})
	return nil
}

func (m *mockClient) MkdirAll(dir string) error {
	if m.mkdirErr != nil {
		return m.mkdirErr
	}
	m.mu.Lock()
	m.mkdirs = append(m.mkdirs, dir)
	m.mu.Unlock()
	return nil
}

func (m *mockClient) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *mockClient) IsReconnecting() bool {
	return false
}

func (m *mockClient) getUploads() []mockUpload {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockUpload, len(m.uploads))
	copy(cp, m.uploads)
	return cp
}

func (m *mockClient) getMkdirs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.mkdirs))
	copy(cp, m.mkdirs)
	return cp
}

func newMockClient() *mockClient {
	return &mockClient{connected: true}
}
