package async

type Worker struct {
	tasks chan func()
	quit  chan struct{}
}

func NewWorker(size int) *Worker {
	w := &Worker{
		tasks: make(chan func(), size),
		quit:  make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *Worker) run() {
	for {
		select {
		case task := <-w.tasks:
			task()
		case <-w.quit:
			return
		}
	}
}

func (w *Worker) Submit(task func()) {
	w.tasks <- task
}

func (w *Worker) Stop() {
	close(w.quit)
}
