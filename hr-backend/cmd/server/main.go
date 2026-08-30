// Package main 是后端单进程启动入口（见 architecture.md 4.2）。
//
// 单进程内嵌：加载配置 → wire 注入 → 起 Gin HTTP + Asynq server + scheduler，
// 捕获信号优雅关闭（先停 HTTP，再停 asynq server/scheduler）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "sili-smart-hr/backend"
	"sili-smart-hr/backend/internal/model"
)

func main() {
	setupSlog("info")

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	a, err := app.InitializeApp(configPath)
	if err != nil {
		slog.Error("initialize app failed", "err", err)
		os.Exit(1)
	}
	// 按配置级别重设日志。
	setupSlog(a.Config.Log.Level)

	// 生产（非 SQLite）禁止使用开发兜底密钥：默认密钥已在源码公开，可离线伪造任意 token。
	if !a.Config.IsSQLite() && a.Config.IsDefaultJWTSecret() {
		slog.Error("refusing to start: non-sqlite database requires JWT_SECRET env override; default secret is public and forgeable")
		os.Exit(1)
	}

	// 生产（非 SQLite）禁止使用开发兜底 LLM 密钥：默认密钥已在源码公开，AES 派生密钥可离线推算。
	if !a.Config.IsSQLite() && a.Config.IsDefaultLLMSecretKey() {
		slog.Error("refusing to start: non-sqlite database requires LLM_SECRET_KEY env override; current key is the public default")
		os.Exit(1)
	}

	// Redis 是 Asynq 硬依赖，启动前必须可达。
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := a.Redis.Ping(pingCtx).Err(); err != nil {
		pingCancel()
		slog.Error("redis unreachable, asynq cannot start", "addr", a.Config.Redis.Addr, "err", err)
		os.Exit(1)
	}
	pingCancel()

	// 记录进程启动时间，供系统状态摘要（GET /api/system/status）消费。
	model.StartedAt = time.Now()

	// 启动 Asynq worker server（消费任务）。
	go func() {
		slog.Info("asynq server starting", "concurrency", a.Config.Asynq.Concurrency)
		a.AsynqServer.Run(a.Mux)
	}()

	// scheduler 启动失败通过 fatalErr 通知主 goroutine 触发整体退出，
	// 避免定时任务静默停止而 /health 仍报 ok。
	fatalErr := make(chan error, 1)
	go func() {
		slog.Info("asynq scheduler starting")
		if err := a.Scheduler.Run(); err != nil {
			slog.Error("asynq scheduler stopped with error", "err", err)
			fatalErr <- err
		}
	}()

	httpServer := &http.Server{
		Addr:              string(a.HTTPAddr),
		Handler:           a.Engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		slog.Info("http server starting", "addr", a.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	// 优雅关闭：先停 HTTP，再停 asynq server/scheduler。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig.String())
	case err := <-fatalErr:
		slog.Error("shutting down due to component failure", "err", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "err", err)
	}
	a.AsynqServer.Shutdown()
	a.Scheduler.Shutdown()
	_ = a.AsynqClient.Close()
	if sqlDB, err := a.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	_ = a.Redis.Close()

	slog.Info("shutdown complete")
}

// setupSlog 配置 JSON 结构化日志到 stdout，级别由 level 控制。
func setupSlog(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
	slog.SetDefault(logger)
}
