package dependency

import (
	"context"
	"log"
	"os"
	"time"
)

var debugDeps = os.Getenv("DEBUG_DEPS") == "true"

type DependencyManager struct {
	services map[string]*ServiceDependency
	ready    chan struct{}
}

type ServiceDependency struct {
	Name         string
	Dependencies []string
	IsReady      bool
	ReadyFunc    func() bool
}

func NewManager() *DependencyManager {
	return &DependencyManager{
		services: make(map[string]*ServiceDependency),
		ready:    make(chan struct{}),
	}
}

func (dm *DependencyManager) Register(name string, deps []string, readyFunc func() bool) {
	dm.services[name] = &ServiceDependency{
		Name:         name,
		Dependencies: deps,
		ReadyFunc:    readyFunc,
	}
}

func (dm *DependencyManager) WaitForDependencies(ctx context.Context) error {
	if len(dm.services) == 0 {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if dm.checkDependencies() {
				close(dm.ready)
				return nil
			}
		}
	}
}

func (dm *DependencyManager) checkDependencies() bool {
	if debugDeps {
		log.Printf("[DEBUG] checking dependencies...")
	}
	for name, svc := range dm.services {
		if !svc.IsReady {
			if debugDeps {
				log.Printf("[DEBUG] checking service %q", name)
			}

			// 检查依赖
			depsReady := true
			for _, dep := range svc.Dependencies {
				if depSvc, ok := dm.services[dep]; !ok || !depSvc.IsReady {
					if debugDeps {
						log.Printf("[DEBUG] dependency %q not ready for %q", dep, name)
					}
					depsReady = false
					break
				}
			}

			// 检查服务就绪状态
			if depsReady && svc.ReadyFunc() {
				if debugDeps {
					log.Printf("[DEBUG] service %q is now ready", name)
				}
				svc.IsReady = true
			} else {
				return false
			}
		}
	}
	return true
}
