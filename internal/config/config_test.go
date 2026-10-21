package config

import (
	"testing"
	"time"
)

func TestValidateLoopbackOnly(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7788": true,
		"localhost:7788": true,
		"[::1]:7788":     true,
		"0.0.0.0:7788":   false,
		":7788":          false,
		"10.0.0.1:7788":  false,
	}
	for addr, ok := range cases {
		err := Config{Addr: addr, HerdrCmd: []string{"herdr"}}.Validate()
		if (err == nil) != ok {
			t.Errorf("Validate(%q) err=%v, want ok=%v", addr, err, ok)
		}
	}
}

func TestReportFlags(t *testing.T) {
	t.Setenv("LC_LEADER_REPORTS", "")
	for _, tc := range []struct {
		args     []string
		enabled  bool
		projects []string
	}{
		{nil, true, nil},
		{[]string{"-leader-reports", "off"}, false, nil},
		{[]string{"-leader-reports", "all"}, true, nil},
		{[]string{"-leader-reports", " a, b ,"}, true, []string{"a", "b"}},
	} {
		cfg, err := Load(tc.args)
		if err != nil || cfg.Reports != tc.enabled || len(cfg.ReportProjects) != len(tc.projects) || cfg.ReportGrace != 5*time.Minute {
			t.Fatalf("%v: %+v %v", tc.args, cfg, err)
		}
		for i := range tc.projects {
			if cfg.ReportProjects[i] != tc.projects[i] {
				t.Fatalf("%v: %v", tc.args, cfg.ReportProjects)
			}
		}
	}
	// Review R6: an explicit but empty or invalid list is a configuration error, not "all".
	for _, v := range []string{"", " ", ", ,", ",", "../x", "a,b c"} {
		if cfg, err := Load([]string{"-leader-reports", v}); err == nil {
			t.Fatalf("%q accepted as %+v", v, cfg)
		}
	}
	if cfg, err := Load([]string{"-report-grace", "0s"}); err != nil || cfg.ReportGrace != 0 {
		t.Fatal(cfg, err)
	}
	if _, err := Load([]string{"-report-grace", "-1m"}); err == nil {
		t.Fatal("negative grace accepted")
	}
	t.Setenv("LC_LEADER_REPORTS", "off")
	if cfg, _ := Load(nil); cfg.Reports {
		t.Fatal("env ignored")
	}
}
