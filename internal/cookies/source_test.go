package cookies

import (
	"sync"
	"testing"
	"time"
)

func TestSourceArbiterAutoPriorities(t *testing.T) {
	a := NewSourceArbiter(CookieSourceModeAuto, true)
	if got := a.SelectedSource(true); got != CookieSourceExternal {
		t.Fatalf("initial source=%q", got)
	}
	if !a.AllowExternalSync() {
		t.Fatal("external source should run before managed authentication")
	}
	a.SetManagedAuthenticated(true)
	if got := a.SelectedSource(true); got != CookieSourceManaged {
		t.Fatalf("managed source=%q", got)
	}
	if a.AllowExternalSync() {
		t.Fatal("external sync should pause while managed is authenticated")
	}
	a.SetManagedAuthenticated(false)
	if got := a.SelectedSource(true); got != CookieSourceExternal {
		t.Fatalf("fallback source=%q", got)
	}
}

func TestSourceArbiterExplicitModesAreIndependent(t *testing.T) {
	cases := []struct {
		mode         string
		managed      bool
		external     bool
		selected     string
		managedRoute bool
		externalSync bool
	}{
		{CookieSourceModeManaged, true, true, CookieSourceManaged, true, false},
		{CookieSourceModeExternal, true, true, CookieSourceExternal, false, true},
		{CookieSourceModeFile, true, true, CookieSourceFile, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			a := NewSourceArbiter(tc.mode, tc.external)
			a.SetManagedAuthenticated(tc.managed)
			if got := a.SelectedSource(true); got != tc.selected {
				t.Fatalf("source=%q want=%q", got, tc.selected)
			}
			if got := a.ManagedInteractiveEnabled(); got != tc.managedRoute {
				t.Fatalf("managed route=%t", got)
			}
			if got := a.AllowExternalSync(); got != tc.externalSync {
				t.Fatalf("external sync=%t", got)
			}
		})
	}
}

func TestSourceArbiterOperationLockAndGateRecheck(t *testing.T) {
	a := NewSourceArbiter(CookieSourceModeAuto, true)
	release := a.LockOperation()

	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		releaseExternal, allowed := a.BeginExternal()
		if allowed {
			releaseExternal()
		}
		done <- allowed
	}()
	<-started
	a.SetManagedAuthenticated(true)
	release()

	select {
	case allowed := <-done:
		if allowed {
			t.Fatal("gate should be rechecked after waiting for operation lock")
		}
	case <-time.After(time.Second):
		t.Fatal("external gate remained blocked")
	}
}

func TestSourceArbiterSerializesOperations(t *testing.T) {
	a := NewSourceArbiter(CookieSourceModeAuto, true)
	var mu sync.Mutex
	inside := 0
	maxInside := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := a.LockOperation()
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("max concurrent operations=%d", maxInside)
	}
}
