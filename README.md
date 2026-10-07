# error-symbolication-go

把线上压缩/合并后的错误堆栈还原到源码位置的进程内符号化库。整条链路只在本
进程内完成：映射表通过本地文件或内存注入，不依赖任何外部服务，固定输入下结果
可稳定复现。

## 组成

| 包 | 职责 |
| --- | --- |
| `sourcemap` | 解析 source map v3（base64 VLQ），按生成位置二分查找原始位置 |
| `stacktrace` | 解析多种运行时的调用栈文本（V8/Chrome/Node、Firefox/Safari） |
| `symbolication` | 版本化映射存储、原子装载、并发限流与容量控制、整栈还原 |

## 快速开始

```go
s := symbolication.NewStore()

// 内联（内存注入）来源
s.Load("2024.05.1", "app.min.js", mapBytes)
// 或本地文件来源
_ = s.LoadFile("2024.05.1", "vendor.min.js", "./maps/vendor.min.js.map")

res := s.Resolve("2024.05.1", rawTrace)
for _, f := range res.Frames {
    fmt.Println(f.Status, f.Source, f.Line, f.Column, f.Function)
}
```

## 还原语义

每个帧独立还原，单帧失败不会丢掉整条调用栈。`ResolvedFrame.Status` 取值：

| Status | 含义 |
| --- | --- |
| `resolved` | 命中映射，`Source/Line/Column/Function` 为原始位置（行 1 起、列 0 起） |
| `no_map` | 该版本或产物没有可用映射（含被容量驱逐） |
| `no_mapping` | 映射存在但该生成位置未被任何段覆盖 |
| `corrupt_map` | 映射内容格式错误（坏 JSON、非法 VLQ 字符、索引越界等） |
| `truncated_map` | 映射内容被截断（JSON 未闭合、VLQ 序列中断） |
| `unparseable_line` | 该行不是可识别的栈帧（如错误消息行、native 帧） |

- 未命中的帧保留原始文本（`Raw`）与解析出的生成位置，绝不返回编造的位置。
- 帧缺列号时按列 0 尽力还原；缺行号/文件时标注 `unparseable_line`。
- 映射表提供原始函数名时优先使用，否则保留帧自带函数名。
- 损坏/截断的映射只影响它自身对应的 (版本, 产物)，不污染同版本其它产物或其它版本。

## 版本匹配规则

- 还原严格按 `Resolve(version, trace)` 指定的版本取映射，同一条错误永远使用它
  产生时对应的代码版本，不会跨版本混用。
- 帧的生成文件先按完整字符串精确匹配产物名，未命中再按路径 basename 匹配，
  规则确定、可复现。
- 装载是原子的：`Load` 先完整解析再以 copy-on-write 方式整体替换快照，装载
  中断或失败不会留下半截可用的映射；查询方任一时刻只能看到某个已提交的完整
  版本，不会读到字段级混合状态。
- 固定输入 + 相同映射版本 ⇒ 结果逐条一致；任意并发度、任意处理顺序不改变单条
  还原结果。

## 配置项

`symbolication.NewStoreWith(symbolication.Options{...})`：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `MaxConcurrent` | 64 | 并发还原上限（信号量），超出者排队，慢请求不占满全部槽位 |
| `MaxMapBytes` | 256 MiB | 已装载映射源内容的总字节预算，超出按 LRU 驱逐，被驱逐者报 `no_map` |
| `MaxTraceBytes` | 1 MiB | 单条堆栈大小上限，超限返回 `ErrTraceTooLarge`，单次还原内存不随映射总量增长 |

解析后的映射不可变且可并发读；还原过程不复制映射内容，长时间运行无额外内存
或句柄增长。

## 测试

```sh
go test ./... -race
```

覆盖：跨版本还原、损坏与截断映射及其隔离性、并发装载与查询的一致性、
并发下结果确定性、容量驱逐、命中/未命中/缺信息帧、文件装载失败回滚。
