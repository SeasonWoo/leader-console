BIN      := bin/leader-console
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X leader-console/internal/server.Version=$(VERSION)
ADDR     ?= 127.0.0.1:7788

.PHONY: build test race vet fmt check e2e run run-fixture clean

build: ## 编译单二进制到 bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/leader-console

test: ## 单元测试
	go test ./...

race: ## 带竞态检测的测试
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal web

check: vet ## 合并前门禁：gofmt 无输出 + vet + race 测试 + 构建
	@test -z "$$(gofmt -l cmd internal web)" || (gofmt -l cmd internal web; echo 'gofmt needed'; exit 1)
	go test -race ./...
	go build ./...

e2e: build ## 端到端：夹具副本 + 假 herdr 启动二进制，curl 走作答回传全链路（需 curl、jq）
	./scripts/e2e.sh $(BIN)

run: build ## 读真实 ~/.leader
	./$(BIN) -addr $(ADDR)

run-fixture: build ## 读 testdata 夹具 + 假 herdr，开发前端/联调用
	./$(BIN) -addr $(ADDR) -leader-root $(CURDIR)/testdata/fixtures/leader-root -herdr-cmd $(CURDIR)/testdata/fake-herdr.sh

clean:
	rm -rf bin
