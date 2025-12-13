package pipeline

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestCollector(t *testing.T) {
	log, _ := zap.NewDevelopment()
	eventChan := make(chan RawTrafficEvent, 10)
	collector := NewCollector(eventChan, log)

	event := RawTrafficEvent{
		SourceIP:      "192.168.1.1",
		DestinationIP: "8.8.8.8",
		Domain:        "google.com",
		Port:          443,
		Timestamp:     time.Now(),
		LatencyMs:     100,
		BytesIn:       1024,
		BytesOut:      512,
		Protocol:      "tcp",
	}

	err := collector.Collect(event)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify event was collected
	select {
	case received := <-eventChan:
		if received.SourceIP != event.SourceIP {
			t.Errorf("expected source IP %s, got %s", event.SourceIP, received.SourceIP)
		}
		if received.Port != event.Port {
			t.Errorf("expected port %d, got %d", event.Port, received.Port)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWorkerPool(t *testing.T) {
	log, _ := zap.NewDevelopment()
	pool := NewWorkerPool(4, log)
	pool.Start()

	var counter int
	var counterMutex sync.Mutex
	tasks := 10

	for i := 0; i < tasks; i++ {
		err := pool.Submit(func() error {
			counterMutex.Lock()
			defer counterMutex.Unlock()
			counter++
			return nil
		})
		if err != nil {
			t.Fatalf("failed to submit task: %v", err)
		}
	}

	pool.Stop()

	counterMutex.Lock()
	defer counterMutex.Unlock()
	if counter != tasks {
		t.Errorf("expected %d tasks to complete, got %d", tasks, counter)
	}
}

func TestConnectionPool(t *testing.T) {
	log, _ := zap.NewDevelopment()
	pool := NewConnectionPool(5, log)

	// Add 5 connections
	for i := 0; i < 5; i++ {
		if !pool.AddConnection() {
			t.Errorf("failed to add connection %d", i+1)
		}
	}

	// Try to add one more (should fail)
	if pool.AddConnection() {
		t.Error("expected to fail adding 6th connection, but succeeded")
	}

	// Remove one and try again
	pool.RemoveConnection()
	if !pool.AddConnection() {
		t.Error("failed to add connection after removal")
	}

	if pool.GetActiveConnections() != 5 {
		t.Errorf("expected 5 active connections, got %d", pool.GetActiveConnections())
	}
}
