# Codex Sessions 十分钟上手教程

这份教程面向第一次使用 `csessions` 的 Linux/WSL 用户。完成后，你会知道会话实际存在哪里，
如何浏览、搜索、预览和恢复会话，以及可选 AI enrichment 和两种“删除”的真实含义。

## 1. 先理解数据关系

Codex CLI 把原生会话保存在 `~/.codex/sessions`。这些 JSONL 文件始终是唯一事实来源，
`csessions` 不会修改或删除它们。

`csessions` 另外建立一个可丢弃的 SQLite 索引，用于快速分组和搜索：

- 默认位置是 `~/.local/share/csessions/sessions.db`；
- 索引只保存脱敏后的用户/助手文字，不保存 reasoning、工具参数或工具输出；
- 预览时才读取原生 JSONL，并默认遮盖疑似秘密；
- 删除这个索引不会删除 Codex 会话，之后可以重新建立。

除了明确执行 `csessions enrich --yes`，其他正常命令都不会访问网络。

## 2. 安装并确认版本

需要 Linux 或 WSL，并且已经可以在终端中运行 `codex`。

下载并检查公开安装脚本，然后安装到个人目录：

```zsh
curl -fsSLO https://raw.githubusercontent.com/9penny/codex-sessions/v0.3.1/install.sh
less install.sh
INSTALL_DIR="$HOME/.local/bin" bash install.sh
```

如果 `~/.local/bin` 尚未在 `PATH` 中，可先在当前终端执行：

```zsh
export PATH="$HOME/.local/bin:$PATH"
```

确认安装结果：

```zsh
csessions version
```

应看到 `Codex Sessions 0.3.1` 或更新的受支持版本。

## 3. 第一次建立索引

运行：

```zsh
csessions reindex
```

它会扫描 `~/.codex/sessions`、跳过没有用户消息的文件，并把可用会话写入派生索引。
再次执行时，未变化的会话会被快速跳过。

`csessions resume` 在索引缺失或为空时也会自动建立索引，但第一次显式运行 `reindex`
更容易看清找到了多少会话。只有排查解析或索引升级问题时才通常需要：

```zsh
csessions reindex --force
```

`--force` 会重新解析全部原生会话，但仍不会修改原生 JSONL。

## 4. 从项目目录浏览和恢复

进入一个平时使用 Codex 的项目，然后打开浏览器：

```zsh
cd ~/Projects/example
csessions resume
```

如果索引中存在该目录启动的会话，界面会优先显示当前项目。常用按键：

| 按键 | 行为 |
|---|---|
| `↑` / `↓` | 移动选择 |
| `space` | 打开默认脱敏的原生会话预览 |
| `R` | 仅在当前预览中临时显示未遮盖文字 |
| `enter` 或 `r` | 在记录的项目目录中运行 `codex resume <id>` |
| `n` | 在选中项目目录中启动新的 Codex 会话 |
| `/` | 在当前界面范围内搜索 |
| `h` | 显示或隐藏 subagent、`exec` 和未知来源会话 |
| `d` | 从 csessions 派生索引中软删除 |
| `tab` | 在当前项目和全部项目界面之间切换（可用时） |
| `q` 或 `esc` | 退出或返回上一层 |

按 `R` 可能显示原生会话中的敏感内容。它只存在于当前进程内存中；切换会话、关闭预览或退出
都会清除这次临时显示。

如果记录的项目目录已不存在，csessions 会阻止 resume，而不会悄悄改用当前目录。

## 5. 浏览全部项目和搜索

从 home 目录运行 `csessions resume`，或在项目界面按 `tab`，可以进入全部项目浏览器：

```zsh
cd ~
csessions resume
```

项目按照最近活动排序，目前是平铺列表而不是目录树。使用 `p` 过滤项目，进入项目后再选择会话。

若已经知道关键词，可直接从全部索引开始搜索：

```zsh
csessions search "数据库连接"
csessions search "retry policy"
```

这里与 TUI 内的 `/` 有一个重要区别：

- `csessions search QUERY` 从全部已索引项目开始；
- TUI 内的 `/` 搜索当前界面所处的范围。

搜索覆盖脱敏后的用户/助手文字，也支持连续中文和技术标识符。存在最新 AI metadata 时，
生成的标题、摘要和标签也参与搜索，并用 `✦ AI-generated` 标识。

## 6. 可选：生成 AI 标题和摘要

这一步不是使用浏览、搜索或 resume 的前提。只有 live enrichment 会访问你配置的 API。

推荐用编辑器创建私有配置文件，避免把 key 留在 shell 历史中：

```zsh
mkdir -p "$HOME/.config/csessions"
chmod 700 "$HOME/.config/csessions"
${EDITOR:-vi} "$HOME/.config/csessions/ai.toml"
chmod 600 "$HOME/.config/csessions/ai.toml"
```

TOML 字符串必须带引号：

```toml
api_key = "YOUR_API_KEY"
base_url = "https://api.openai.com/v1"
model = "gpt-5.4-mini"
```

`base_url` 和 `model` 可以替换为你的兼容 endpoint 和可用模型。不要把真实 key 写入项目文件、
Git 仓库、命令参数或教程示例。

先确认 endpoint 暴露的模型；这一步会发起网络请求，但不会发送会话内容：

```zsh
csessions enrich models
```

然后在目标项目目录进行完全本地的 dry run：

```zsh
cd ~/Projects/example
csessions enrich --dry-run --limit 10
```

dry run 会读取、解析和脱敏原生会话，但请求数和写入数都为零。输出中的字段含义是：

- `candidates`：经过项目范围、fingerprint 和 `--limit` 筛选的候选数；
- `prepared`：进一步通过解析、脱敏和总输入预算的实际批次数；
- `estimated_input_tokens`：保守的本地输入估算。

默认每条最多估算 2000 input tokens，总预算是 10000。因此即使有 10 个 `candidates`，
也可能只有 5 个 `prepared`。这是总预算生效，不是漏掉了会话。

确认 dry run 后，显式允许发送脱敏后的用户/助手文字：

```zsh
csessions enrich --yes --limit 10
```

默认只处理当前项目。其他范围必须明确指定：

```zsh
csessions enrich --project ~/Projects/another-project --yes --limit 10
csessions enrich --all --yes --limit 10
```

API provider 仍可能按照自己的政策保留 abuse-monitoring 日志；`store: false` 不会覆盖
provider 的政策。完整边界见 [AI enrichment 指南](V0.2-ENRICHMENT.md)。

## 7. 区分两种删除

Codex 和 csessions 的“删除”作用在不同数据层：

| 操作 | 影响原生 JSONL | 影响 csessions 索引 | 恢复方式 |
|---|---:|---:|---|
| 在 csessions 中按 `d` | 否 | 建立 tombstone 并隐藏该条目 | 当前版本需要重建整个派生数据库 |
| 在 Codex 中执行 `/delete` | 是，永久删除保存的会话 | 旧索引行暂时可能仍在 | 成功运行 `csessions reindex` 后清理旧索引行 |

普通或 `--force` reindex 都不会恢复 csessions 的 `d` tombstone。如果确实要恢复所有仍有原生
JSONL 的软删除条目，可以先关闭 csessions，备份整个派生数据目录，再重建：

```zsh
index_backup="$HOME/.local/share/csessions.backup.$(date +%Y%m%d-%H%M%S)"
mv "$HOME/.local/share/csessions" "$index_backup"
csessions reindex
```

如果设置了 `XDG_DATA_HOME`，实际目录是 `$XDG_DATA_HOME/csessions`。重建会丢失当前索引中的
AI metadata，但备份目录仍可用于回滚。当前版本还没有恢复单条 tombstone 的命令。

## 8. 常见问题

### `Ai API key is not configured`

浏览、搜索和 resume 不需要 API key。只有 live enrichment 需要。检查 `ai.toml` 是否位于
正确的 XDG config 目录，或者是否设置了 `CSESSIONS_OPENAI_API_KEY`。

### TOML 报 `expected value but found "sk"`

API key 没有放在双引号中。正确写法是 `api_key = "..."`，并确保文件权限是 `0600`。

### Codex `/delete` 后，`csessions resume` 仍显示旧会话

运行一次 `csessions reindex`。只有完整成功的扫描才会根据原生库存清理旧索引；扫描失败时
会保守地保留现有记录，避免误删。

### enrichment 提示 source changed

原生会话在索引之后发生了变化。先运行 `csessions reindex`，再重新 dry run 或 enrich。

### resume 提示项目目录不存在

csessions 不会在错误目录恢复会话。恢复或重新挂载原目录；否则只能从仍存在的项目目录启动
新会话。

## 9. 推荐的日常流程

通常只需要：

```zsh
cd ~/Projects/example
csessions resume
```

当原生会话有明显变化、执行过 Codex `/delete`，或者 enrichment 报 fingerprint 变化时：

```zsh
csessions reindex
```

想给当前项目补充新摘要时，先 dry run，再显式执行 live enrichment：

```zsh
csessions enrich --dry-run --limit 10
csessions enrich --yes --limit 10
```

到这里，你已经覆盖了 v0.3.1 的完整核心工作流。
