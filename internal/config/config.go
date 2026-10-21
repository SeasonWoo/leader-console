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
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:          *addr,
		LeaderRoot:    *root,
		HerdrCmd:      strings.Fields(*herdrCmd),
		ScanInterval:  *scan,
		HerdrInterval: *herdrIv,
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
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
