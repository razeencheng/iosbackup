package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cmdIdeviceAssertion           = "/usr/local/bin/ideviceassertion"
	powerAssertionSeconds         = 1200
	defaultPowerAssertionRenew    = 10 * time.Minute
	defaultPowerAssertionAcquire  = 35 * time.Second
	powerAssertionShutdownTimeout = 5 * time.Second
)

var errPowerAssertionExited = errors.New("Wi-Fi power assertion 提前退出")
var errPowerAssertionLost = errors.New("Wi-Fi power assertion 已丢失")

type powerAssertionLease interface {
	Done() <-chan error
	Release()
}

type powerAssertionStarter interface {
	Start(ctx context.Context, udid string, env []string) (powerAssertionLease, error)
}

type execPowerAssertionStarter struct {
	acquireTimeout time.Duration
}

type execPowerAssertionLease struct {
	cancel      context.CancelFunc
	done        chan error
	processDone chan struct{}
	releaseOnce sync.Once
}

func (l *execPowerAssertionLease) Done() <-chan error { return l.done }

func (l *execPowerAssertionLease) Release() {
	l.releaseOnce.Do(l.cancel)
	select {
	case <-l.processDone:
	case <-time.After(powerAssertionShutdownTimeout):
	}
}

func (s execPowerAssertionStarter) Start(ctx context.Context, udid string, extraEnv []string) (powerAssertionLease, error) {
	acquireTimeout := s.acquireTimeout
	if acquireTimeout <= 0 {
		acquireTimeout = defaultPowerAssertionAcquire
	}

	procCtx, cancel := context.WithCancel(ctx)
	args := []string{
		"-n", "-u", udid,
		"--timeout", fmt.Sprint(powerAssertionSeconds),
		"--hold", fmt.Sprint(powerAssertionSeconds),
		"--name", "iOS Backup",
		"--detail", "mobilebackup2 Wi-Fi backup",
	}
	cmd := newExecCmd(procCtx, cmdIdeviceAssertion, args...)
	cmd.SetGracefulCancel(3 * time.Second)
	cmd.SetEnv(append(os.Environ(), extraEnv...))

	stdout, err := cmd.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("创建 assertion 输出管道: %w", err)
	}
	var stderr bytes.Buffer
	cmd.SetStderr(&stderr)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("启动 assertion helper: %w", err)
	}

	lease := &execPowerAssertionLease{
		cancel:      cancel,
		done:        make(chan error, 1),
		processDone: make(chan struct{}),
	}
	waitResult := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		waitResult <- err
		lease.done <- err
		close(lease.processDone)
	}()

	acquired := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "ASSERTION_ACQUIRED ") {
				select {
				case acquired <- struct{}{}:
				default:
				}
			}
		}
	}()

	timer := time.NewTimer(acquireTimeout)
	defer timer.Stop()
	select {
	case <-acquired:
		return lease, nil
	case err := <-waitResult:
		cancel()
		if err == nil {
			err = errPowerAssertionExited
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = "helper exited before ASSERTION_ACQUIRED"
		}
		return nil, fmt.Errorf("获取 Wi-Fi power assertion 失败: %s: %w", message, err)
	case <-timer.C:
		lease.Release()
		return nil, fmt.Errorf("获取 Wi-Fi power assertion 超时（%s）", acquireTimeout)
	case <-ctx.Done():
		lease.Release()
		return nil, context.Cause(ctx)
	}
}

type wifiPowerAssertionSession struct {
	cancel      context.CancelFunc
	done        chan error
	workerDone  chan struct{}
	releaseOnce sync.Once
	releasing   atomic.Bool
}

func (s *wifiPowerAssertionSession) Done() <-chan error { return s.done }

func (s *wifiPowerAssertionSession) Release() {
	if s == nil {
		return
	}
	s.releaseOnce.Do(func() {
		s.releasing.Store(true)
		s.cancel()
		<-s.workerDone
	})
}

func (app *application) startWiFiPowerAssertion(ctx context.Context, udid string, isNetwork bool) (*wifiPowerAssertionSession, error) {
	if !isNetwork || !app.runtimeConfig.WiFiPowerAssertion {
		return nil, nil
	}
	starter := app.powerAssertionStarter
	if starter == nil {
		starter = execPowerAssertionStarter{}
	}
	env := app.networkIdeviceEnvVars()
	first, err := starter.Start(ctx, udid, env)
	if err != nil {
		return nil, err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &wifiPowerAssertionSession{
		cancel:     cancel,
		done:       make(chan error, 1),
		workerDone: make(chan struct{}),
	}
	renewInterval := app.powerAssertionRenewInterval
	if renewInterval <= 0 {
		renewInterval = defaultPowerAssertionRenew
	}

	go func() {
		defer close(session.workerDone)
		current := first
		defer current.Release()
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()

		for {
			select {
			case <-sessionCtx.Done():
				return
			case leaseErr := <-current.Done():
				if session.releasing.Load() || sessionCtx.Err() != nil {
					return
				}
				if leaseErr == nil {
					leaseErr = errPowerAssertionExited
				}
				session.done <- leaseErr
				return
			case <-ticker.C:
				next, renewErr := starter.Start(sessionCtx, udid, env)
				if renewErr != nil {
					app.addWarnLog(udid, fmt.Sprintf("Wi-Fi power assertion 续租失败，继续保留旧租约: %v", renewErr))
					continue
				}
				old := current
				current = next
				old.Release()
				app.addInfoLog(udid, "Wi-Fi power assertion 已续租")
			}
		}
	}()

	app.addInfoLog(udid, "Wi-Fi power assertion 已建立")
	return session, nil
}

func (app *application) guardWiFiBackupPowerAssertion(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	udid string,
	isNetwork bool,
) (func(), error) {
	session, err := app.startWiFiPowerAssertion(ctx, udid, isNetwork)
	if err != nil {
		return nil, fmt.Errorf("建立 Wi-Fi power assertion: %w", err)
	}
	if session == nil {
		return func() {}, nil
	}
	go func() {
		select {
		case assertionErr := <-session.Done():
			if assertionErr == nil {
				assertionErr = errPowerAssertionExited
			}
			cancel(fmt.Errorf("%w: %v", errPowerAssertionLost, assertionErr))
		case <-ctx.Done():
		}
	}()
	return session.Release, nil
}
