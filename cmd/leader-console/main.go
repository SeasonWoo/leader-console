// Command leader-console 是本机 leader 管控面：只监听 127.0.0.1，浏览器访问。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"leader-console/internal/config"
	"leader-console/internal/herdr"
	"leader-console/internal/ledgercompat"
	"leader-console/internal/model"
	"leader-console/internal/reconcile"
	"leader-console/internal/redact"
	"leader-console/internal/render"
	"leader-console/internal/server"
	"leader-console/internal/service"
	"leader-console/internal/store"
	"leader-console/web"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	fsStore, err := store.NewFS(cfg.LeaderRoot, store.FSOptions{})
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	ready := &readyStore{Store: fsStore, ready: make(chan struct{})}
	svc := service.New(service.Deps{
		Store: ready,
		Herdr: herdr.NewExec(cfg.HerdrCmd, 5*time.Second),
		Reconcile: func(pd *model.ProjectData, agents []herdr.Agent, herdrOK bool, now time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
			r := reconcile.Project(pd, agents, herdrOK, now)
			return r.Leader, r.Tasks
		},
		Redact:  redact.String,
		Render:  render.Markdown,
		ToMerge: ledgercompat.LikelyToMerge,
		Now:     time.Now,
	}, service.Options{
		ScanInterval:  cfg.ScanInterval,
		HerdrInterval: cfg.HerdrInterval,
		Reports:       service.ReportOptions{Enabled: cfg.Reports, Projects: cfg.ReportProjects, Grace: cfg.ReportGrace, StartupDelay: cfg.ReportStartupDelay},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run 先取得单实例锁再首次扫描；首次调用 ListProjects 即说明锁已到手，此后才接受写请求。
	runErr := make(chan error, 1)
	go func() { runErr <- svc.Run(ctx) }()
	select {
	case err := <-runErr:
		log.Fatalf("service: %v", err)
	case <-ready.ready:
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(svc, web.Static(), server.Options{Addr: cfg.Addr}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("leader-console %s listening on http://%s (leader root %s)", server.Version, cfg.Addr, cfg.LeaderRoot)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	failed, runDone := false, false
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		log.Printf("listen: %v", err)
		failed = true
	case err := <-runErr:
		// 后台服务退出后不再持有实例锁，HTTP 也必须一起停。
		log.Printf("service stopped: %v", err)
		failed, runDone = true, true
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	if !runDone {
		if err := <-runErr; err != nil {
			log.Printf("service: %v", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// readyStore 在 service 第一次扫描时关闭 ready，用来确认 Run 已取得实例锁。
type readyStore struct {
	store.Store
	once  sync.Once
	ready chan struct{}
}

func (s *readyStore) ListProjects(ctx context.Context) ([]string, error) {
	s.once.Do(func() { close(s.ready) })
	return s.Store.ListProjects(ctx)
}
