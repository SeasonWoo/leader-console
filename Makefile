BIN      := bin/leader-console
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X leader-console/internal/server.Version=$(VERSION)
ADDR     ?= 127.0.0.1:7788
# 前端包管理器：优先用本机 pnpm，否则用 npx 拉取 package.json 里锁定的版本。
PNPM_VERSION := 10.34.7
PNPM     ?= $(shell command -v pnpm >/dev/null 2>&1 && echo pnpm || echo npx --yes pnpm@$(PNPM_VERSION))
WEB_DIST := web/dist
export NEXT_TELEMETRY_DISABLED := 1

.PHONY: build go-build web web-deps web-test web-verify web-dev test race vet fmt check e2e run run-fixture clean

build: web go-build ## 构建前端并编译单二进制到 bin/（前端产物嵌入二进制）

go-build: ## 只编译 Go（使用已入库的 web/dist，不需要 Node）
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/leader-console

web-deps:
	cd frontend && $(PNPM) install --frozen-lockfile

web: web-deps ## 构建前端（Next.js 静态导出）并同步到 web/dist
	cd frontend && $(PNPM) run build
	rm -rf $(WEB_DIST)
	cp -R frontend/out $(WEB_DIST)

web-test: web-deps ## 前端类型检查 + 单元测试（node --test）
	cd frontend && $(PNPM) run typecheck && $(PNPM) run test

web-verify: web ## 校验已入库的 web/dist 与前端源码一致（合并前跑）
	@git diff --quiet --exit-code -- $(WEB_DIST) && test -z "$$(git ls-files --others --exclude-standard -- $(WEB_DIST))" || \
		(git status --short -- $(WEB_DIST); echo 'web/dist 与源码不一致：请提交 make web 的产物'; exit 1)

web-dev: web-deps ## 前端开发服务器（127.0.0.1:17810，配合 ?mock=1 使用）
	cd frontend && $(PNPM) run dev

test: web web-test ## 构建前端 + 前端测试 + Go 单元测试
	go test ./...

race: ## 带竞态检测的测试
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal web

check: vet web-test ## 合并前门禁：gofmt 无输出 + vet + 前端测试 + race 测试 + 构建 + web/dist 与源码一致
	@test -z "$$(gofmt -l cmd internal web)" || (gofmt -l cmd internal web; echo 'gofmt needed'; exit 1)
	go test -race ./...
	go build ./...
	$(MAKE) web-verify

e2e: build ## 端到端：夹具副本 + 假 herdr 启动二进制，curl 走作答回传全链路 + 无头 Chrome 走查三个页面（需 curl、jq、node）
	./scripts/e2e.sh $(BIN)

run: build ## 读真实 ~/.leader
	./$(BIN) -addr $(ADDR)

run-fixture: build ## 读 testdata 夹具 + 假 herdr，开发前端/联调用
	./$(BIN) -addr $(ADDR) -leader-root $(CURDIR)/testdata/fixtures/leader-root -herdr-cmd $(CURDIR)/testdata/fake-herdr.sh

clean:
	rm -rf bin frontend/.next frontend/out
