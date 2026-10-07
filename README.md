# error-symbolication-go

把线上压缩/合并后的错误堆栈在**本进程内**还原到源码位置。输入为本地文件
或内存注入的 Source Map（v3），不依赖任何外部服务，固定输入下结果可稳定复现。

## 结构

| 包 | 职责 |
|---|---|
| `sourcemap` | Source Map v3 解析（base64 VLQ）、生成位置 → 原始位置查询 |
| `symbolicate` | 多格式调用栈解析、逐帧还原 |
| `store` | 按版本管理映射表：原子装载/替换、容量上限与 LRU 淘汰 |

## 快速开始

```go
s := store.New(store.Config{MaxBytes: 256 << 20})

// 三种装载来源（均原子生效）：
_ = s.Load("v1.2.0", "bundle.js", rawJSON)          // 内存注入
_ = s.LoadInline("v1.2.0", "bundle.js", dataURL)    // data:...;base64,...
_ = s.LoadFile("v1.2.0", "bundle.js", "app.js.map") // 本地文件

frames := symbolicate.ParseStack(rawStackTrace)
results := symbolicate.SymbolicateStack("v1.2.0", frames, s)
```

## 还原语义

- `Lookup(line, column)`：行号 1 起始、列号 0 起始，采用**最大下界**匹配
  （同行内不大于目标列的最后一个分段）。命中返回原始文件名、1 起始行号、
  0 起始列号与原始函数名；映射表没有函数名时回退为帧中的压缩后函数名。
- **未命中不编造结果**，原因可区分：
  - `line_out_of_range`：生成行超出映射范围；
  - `no_segment`：该行没有任何分段，或目标列之前没有分段；
  - `unmapped_segment`：分段存在但不携带原始位置信息。
- 帧级状态（`Result.Status`）：
  - `ok`：已还原；
  - `no_map`：该 (版本, 文件) 未注册映射表；
  - `miss`：有映射表但该位置无映射（`Reason` 给出上表原因）；
  - `unparsed`：帧本身缺少可用的文件/行号信息。
- 未还原的帧**保留原始帧信息**，单帧失败不会丢弃整条调用栈。
- 列号缺失的帧按该行第 0 列尽力还原；函数名缺失时以映射表中的名字为准。

## 支持的调用栈格式

- V8/Chrome：`at fn (file:line:col)`、`at file:line:col`、`at async fn (file:line)`；
- Firefox/Safari：`fn@file:line:col`、`@file:line:col`、`fn@file:line`。
- 列号、函数名均可缺失；无法识别的行被跳过，不影响其它帧。

## 版本匹配规则

- 映射表以 `(version, file)` 为键，查询**精确匹配**版本，绝不跨版本回退或混用。
- 同一条错误必须携带其产生时的代码版本调用 `SymbolicateStack(version, ...)`。
- 装载与查询可并发：任一查询要么看到旧版本完整映射，要么看到新版本完整
  映射，绝不会看到字段级混合状态。

## 装载语义与错误分类

- `Load*` 先完整解析再一次性替换，**解析失败或中断不会留下半截映射**，
  旧映射保持可用；坏映射只影响它自己的键，不污染同版本或其它产物。
- 解析失败按 `*sourcemap.ParseError` 分类：
  - `sourcemap.IsCorrupt(err)`：JSON 非法、版本不符、VLQ 含非法字符、
    索引越界等；
  - `sourcemap.IsTruncated(err)`：JSON 意外结束、VLQ 延续位未闭合。

## 配置项

`store.Config`：

| 字段 | 含义 | 默认 |
|---|---|---|
| `MaxBytes` | 全部已解析映射的估算内存上限，超出时按 LRU 淘汰；`0` 表示不限 | `0` |

- 单次还原的内存占用只取决于该次用到的映射，不随映射总量增长；
  解析在加锁前完成，慢装载不阻塞查询。
- 已安装的映射不可变，查询方持有引用期间即使被淘汰/替换也仍然有效，
  无句柄持有（文件读取后即关闭）。
- `Store.Stats()` 返回当前映射数量与估算占用，便于观测。

## 确定性

固定输入与相同映射版本下，还原结果逐条一致；同一批错误以任意并发度、
任意顺序处理，单条结果不变，可复现历史结论。

## 测试

```sh
go test ./... -race
```

覆盖：跨版本还原、损坏/截断映射识别与隔离、并发装载与查询、容量上限
淘汰、命中与各类未命中、端到端版本钉扎。
