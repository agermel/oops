package runtime

import "sync"

var mutationQueues sync.Map

type mutationQueue struct {
	mu sync.Mutex
}

func withFileMutationQueue(path string, fn func() error) error {
	value, _ := mutationQueues.LoadOrStore(path, &mutationQueue{})
	queue := value.(*mutationQueue)
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return fn()
}
