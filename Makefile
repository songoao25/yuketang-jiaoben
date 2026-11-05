APP       := ykt
WAILS     ?= $(HOME)/go/bin/wails
GUI_TAGS  := -tags desktop,production

# macOS 上链接 Wails GUI 需要 UniformTypeIdentifiers（`wails build` 会自动加，裸 go build 不会）
ifeq ($(shell uname -s),Darwin)
GUI_ENV := CGO_LDFLAGS="-framework UniformTypeIdentifiers"
endif

.PHONY: all app cli test vet fmt clean install-cli release

all: app cli

## app: 构建当前平台桌面 App（macOS 产物在 build/bin/ykt.app）
app:
	$(WAILS) build

## cli: 构建纯 Go CLI（无 cgo、无 WebView 依赖，可直接交叉编译到三平台）
cli:
	mkdir -p bin
	go build -tags cli -ldflags "-s -w" -o bin/$(APP) .

## test: 运行 Go 测试（internal 包 + GUI 绑定集成测试）
test:
	go test ./internal/...
	$(GUI_ENV) go test $(GUI_TAGS) .

## vet: 静态检查（CLI 纯 Go 版 + GUI 版）
vet:
	go vet ./internal/...
	go vet -tags cli .
	go vet $(GUI_TAGS) .

fmt:
	gofmt -w *.go internal

## install-cli: 安装 CLI 到 ~/.local/bin/ykt（覆盖旧的 Node 软链）
install-cli: cli
	install -d "$(HOME)/.local/bin"
	install -m 0755 bin/$(APP) "$(HOME)/.local/bin/$(APP)"

## cross-cli: 交叉编译三平台 CLI 到 dist/
cross-cli:
	mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -tags cli -ldflags "-s -w" -o dist/ykt-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -tags cli -ldflags "-s -w" -o dist/ykt-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -tags cli -ldflags "-s -w" -o dist/ykt-linux-amd64 .
	GOOS=windows GOARCH=amd64 go build -tags cli -ldflags "-s -w" -o dist/ykt-windows-amd64.exe .

## release: 打包 GitHub Release 资产（App 自更新用），可用 VERSION=x.y.z 指定版本
release:
	VERSION=$(VERSION) bash tools/package-release.sh

clean:
	rm -rf build/bin bin dist
