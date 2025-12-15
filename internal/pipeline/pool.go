package pipeline

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// WorkerPool manages a pool of workers processing tasks.
type WorkerPool struct {
	taskChan   chan Task
	numWorkers int
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	log        *zap.Logger
}

// Task represents a unit of work to be executed by a worker.
type Task func() error

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(numWorkers int, log *zap.Logger) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		taskChan:   make(chan Task, numWorkers*2),
		numWorkers: numWorkers,
		ctx:        ctx,
		cancel:     cancel,
		log:        log,
	}
}

// Start begins processing tasks.
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker()
	}
}

// worker processes tasks from the task channel.
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			return
		case task := <-wp.taskChan:
			if task == nil {
				return
			}
			if err := task(); err != nil {
				wp.log.Error("task execution failed", zap.Error(err))
			}
		}
	}
}

// Submit submits a task to the worker pool.
func (wp *WorkerPool) Submit(task Task) error {
	select {
	case <-wp.ctx.Done():
		return wp.ctx.Err()
	case wp.taskChan <- task:
		return nil
	}
}

// Stop stops the worker pool and waits for all tasks to complete.
func (wp *WorkerPool) Stop() {
	wp.cancel()
	close(wp.taskChan)
	wp.wg.Wait()
}

// ConnectionPool manages connection pooling for better resource utilization.
type ConnectionPool struct {
	maxConnections int
	activeConn     int
	mu             sync.RWMutex
	log            *zap.Logger
}

// NewConnectionPool creates a new connection pool.
func NewConnectionPool(maxConnections int, log *zap.Logger) *ConnectionPool {
	return &ConnectionPool{
		maxConnections: maxConnections,
		log:            log,
	}
}

// CanAccept checks if a new connection can be accepted.
func (cp *ConnectionPool) CanAccept() bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	return cp.activeConn < cp.maxConnections
}

// AddConnection increments the active connection count.
func (cp *ConnectionPool) AddConnection() bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if cp.activeConn >= cp.maxConnections {
		cp.log.Warn("max connections reached", zap.Int("max", cp.maxConnections))

		return false
	}

	cp.activeConn++

	return true
}

// RemoveConnection decrements the active connection count.
func (cp *ConnectionPool) RemoveConnection() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if cp.activeConn > 0 {
		cp.activeConn--
	}
}

// GetActiveConnections returns the current number of active connections.
func (cp *ConnectionPool) GetActiveConnections() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	return cp.activeConn
}
