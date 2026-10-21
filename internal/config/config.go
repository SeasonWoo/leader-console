// Package config 解析启动参数。优先级：命令行 flag > 环境变量 > 默认值。
package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config 是进程级配置。
type Config struct {
	// Addr 是监听地址，必须是回环地址。
	Addr string
	// LeaderRoot 是 leader 数据根目录，默认 ~/.leader。
	LeaderRoot string
	// HerdrCmd 是调用 herdr 的命令前缀，例如 ["herdr"] 或 ["herdr", "--session", "work"]。
	HerdrCmd []string
	// ScanInterval 是扫描 LeaderRoot 的间隔。
	ScanInterval time.Duration
	// HerdrInterval 是刷新 `herdr agent list` 的间隔。
	HerdrInterval time.Duration
	// Reports 为 false 时面板不定时投递【定期汇报】。
	Reports bool
	// ReportProjects 限定定时投递的项目；空表示所有 lc-1 项目。
	ReportProjects []string
	// ReportGrace 是到期后额外等待的时间，让 leader 自己的 CronCreate 优先；leader 不再自建 cron 后可设 0。
	ReportGrace time.Duration
	// ReportStartupDelay 是面板启动后首次投递前的最短等待，避免重启即投递。
	ReportStartupDelay time.Duration
}

// Load 从 args（不含程序名）和环境变量解析配置。
func Load(args []string) (Config, error) {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("leader-console", flag.ContinueOnError)
	addr := fs.String("addr", envOr("LC_ADDR", "127.0.0.1:7788"), "listen address (loopback only)")
	root := fs.String("leader-root", envOr("LC_LEADER_ROOT", filepath.Join(home, ".leader")), "leader data root")
	herdrCmd := fs.String("herdr-cmd", envOr("LC_HERDR_CMD", "herdr"), "herdr command prefix, split on spaces")
	scan := fs.Duration("scan-interval", 2*time.Second, "leader root scan interval")
	herdrIv := fs.Duration("herdr-interval", 5*time.Second, "herdr agent list refresh interval")
	reports := fs.String("leader-reports", envOr("LC_LEADER_REPORTS", "all"), "periodic 【定期汇报】 to idle, verified leaders: all | off | comma-separated projects")
	grace := fs.Duration("report-grace", 5*time.Minute, "extra wait after a report is due, so a leader's own cron keeps priority (0 once it relies on the panel)")
	startup := fs.Duration("report-startup-delay", time.Minute, "minimum wait after startup before the first report")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:          *addr,
		LeaderRoot:    *root,
		HerdrCmd:      strings.Fields(*herdrCmd),
		ScanInterval:  *scan,
		HerdrInterval: *herdrIv,
		ReportGrace:   *grace,

		ReportStartupDelay: *startup,
	}
	switch v := strings.TrimSpace(*reports); v {
	case "all", "on":
		cfg.Reports = true
	case "off", "":
	default:
		cfg.Reports = true
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.ReportProjects = append(cfg.ReportProjects, p)
			}
		}
	}
	return cfg, cfg.Validate()
}

// Validate 检查配置；非回环监听地址一律拒绝。
func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", c.Addr, err)
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("addr %q is not a loopback address", c.Addr)
		}
	}
	if len(c.HerdrCmd) == 0 {
		return fmt.Errorf("herdr command is empty")
	}
	if c.ReportGrace < 0 || c.ReportStartupDelay < 0 {
		return fmt.Errorf("report-grace and report-startup-delay must not be negative")
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
