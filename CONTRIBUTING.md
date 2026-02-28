# 贡献指南

感谢你对 CodeDebate 的关注！欢迎任何形式的贡献。

## 如何贡献

### 报告 Bug

请通过 [GitHub Issues](https://github.com/longyunfeigu/codedebate/issues) 提交，使用 Bug Report 模板并提供：

- CodeDebate 版本（`codedebate --help` 输出）
- 操作系统和 Go 版本
- 使用的 AI 提供者（claude-code / codex-cli / openai）
- 复现步骤
- 期望行为 vs 实际行为

### 提交功能建议

同样通过 [GitHub Issues](https://github.com/longyunfeigu/codedebate/issues) 提交，使用 Feature Request 模板。

### 提交代码

1. Fork 本仓库
2. 创建功能分支：`git checkout -b feature/your-feature`
3. 确保代码通过编译和测试：
   ```bash
   make build
   make test
   make lint    # 需要安装 golangci-lint
   ```
4. 提交变更：`git commit -m "feat: add your feature"`
5. 推送分支：`git push origin feature/your-feature`
6. 创建 Pull Request

### Commit 规范

使用 [Conventional Commits](https://www.conventionalcommits.org/)：

- `feat:` 新功能
- `fix:` Bug 修复
- `docs:` 文档变更
- `refactor:` 重构（不改变功能）
- `test:` 测试相关
- `chore:` 构建/工具链变更

## 开发环境

```bash
# 依赖
Go 1.24+

# 克隆
git clone https://github.com/longyunfeigu/codedebate.git
cd codedebate

# 构建
make build

# 运行测试
make test

# 本地审查测试
./codedebate review --local
```

## 项目结构

```
cmd/          CLI 命令入口
internal/
  ├── orchestrator/   辩论编排器（核心）
  ├── provider/       AI 提供者抽象层
  ├── platform/       GitHub / GitLab 平台层
  ├── context/        上下文收集
  ├── display/        终端 UI
  ├── server/         Webhook 服务
  └── config/         配置管理
```

## 许可证

贡献的代码将采用与本项目相同的 [MIT License](LICENSE)。
