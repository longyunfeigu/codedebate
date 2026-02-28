BINARY    := codedebate
GO        := go
GOFLAGS   :=
LDFLAGS   :=

.PHONY: build install test lint clean demo

## build: 编译 codedebate 二进制
build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

## install: 安装到 $GOPATH/bin
install:
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" .

## test: 运行测试
test:
	$(GO) test ./... -v

## lint: 代码检查 (需要 golangci-lint)
lint:
	golangci-lint run ./...

## clean: 清理构建产物
clean:
	rm -f $(BINARY)
	$(GO) clean

## demo: 录制终端演示 GIF (需要 vhs)
demo:
	vhs demo/demo.tape

## help: 显示帮助
help:
	@echo "可用命令:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'
