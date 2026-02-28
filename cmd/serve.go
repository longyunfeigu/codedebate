package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/longyunfeigu/codedebate/internal/config"
	"github.com/longyunfeigu/codedebate/internal/server"
	"github.com/spf13/cobra"
)

// serveCmd 定义了 "serve" 子命令，启动 webhook server 接收 GitLab MR 事件并自动触发审查。
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start webhook server for automatic MR reviews",
	Long: `Start an HTTP server that listens for GitLab Merge Request webhook events
and automatically triggers CodeDebate code reviews. Review results are posted back
to the MR as inline comments and a summary note.

Prerequisites:
  - glab CLI must be installed and authenticated (glab auth login or GITLAB_TOKEN env)
  - GitLab webhook must be configured to send MR events to this server`,
	RunE: runServe,
}

func init() {
	f := serveCmd.Flags()
	f.StringP("config", "c", "", "Path to config file")
	f.String("addr", "", "Listen address (default :8080, env CODEDEBATE_ADDR)")
	f.String("webhook-secret", "", "GitLab webhook secret token (required, env CODEDEBATE_WEBHOOK_SECRET)")
	f.Int("max-concurrent", 3, "Maximum concurrent reviews")
	f.String("gitlab-host", "", "GitLab host (default gitlab.com, env GITLAB_HOST)")
}

func runServe(cmd *cobra.Command, args []string) error {
	// 加载配置
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// 解析 addr（flag > env > default）
	addr, _ := cmd.Flags().GetString("addr")
	if addr == "" {
		addr = os.Getenv("CODEDEBATE_ADDR")
	}
	if addr == "" {
		addr = ":8080"
	}

	// 解析 webhook-secret（flag > env）
	webhookSecret, _ := cmd.Flags().GetString("webhook-secret")
	if webhookSecret == "" {
		webhookSecret = os.Getenv("CODEDEBATE_WEBHOOK_SECRET")
	}
	// TODO: 暂时不强制要求 webhook secret
	// if webhookSecret == "" {
	// 	return fmt.Errorf("webhook secret is required (--webhook-secret or CODEDEBATE_WEBHOOK_SECRET)")
	// }

	// 解析 gitlab-host（flag > env > default）
	gitlabHost, _ := cmd.Flags().GetString("gitlab-host")
	if gitlabHost == "" {
		gitlabHost = os.Getenv("GITLAB_HOST")
	}
	if gitlabHost == "" {
		gitlabHost = "gitlab.com"
	}

	maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent")

	srv := server.New(server.ServerConfig{
		CodeDebateConfig:   cfg,
		Addr:          addr,
		WebhookSecret: webhookSecret,
		MaxConcurrent: maxConcurrent,
		GitLabHost:    gitlabHost,
	})

	// Graceful shutdown
	done := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			done <- err
		}
		close(done)
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	case err := <-done:
		return err
	}
}
