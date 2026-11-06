# SOJ JSON Schemas

JSON Schema定义测评包文件的字段、required、enum和格式，是文件格式的唯一权威资产；Proto中的相似对象不定义这些文件。执行、路径与文件安全规则统一见[`judge.md`](../../judge.md)。

| Schema | 文件 | 内容 |
|---|---|---|
| [manifest.schema.json](manifest.schema.json) | 包根目录manifest.json | schemaVersion、problemId及可选Docker执行配置 |
| [job.schema.json](job.schema.json) | Job根目录job.json | schemaVersion、jobId、submission.filename/contentType |
| [result.schema.json](result.schema.json) | Job根目录output/result.json | schemaVersion、jobId、verdict、score及可选message |

三者采用Draft 2020-12，只接受schemaVersion 1，拒绝未知字段；校验器必须启用对应Draft。完整assignment、Artifact元数据、manifest、partition、凭据和固定路径不复制进job.json。

## 校验职责

- Control Plane发布包时验证manifest；Agent执行前验证manifest和生成的job。manifest.problemId必须匹配目标Problem。
- Agent负责原始result的UTF-8 JSON完整消费、Schema与Job ID校验，并从同一份已验证内容生成结构化RPC结果、上传原始bytes归档。Control Plane验证结构化RPC及Artifact绑定条件，原始结果仅归档、不读取解析或执行Schema校验，见[Judge契约](../../judge-contract.md#completejob)。
- score使用精确Decimal字符串，禁止转换为浮点数；其词法约束由result Schema定义。`jobId`为正整数；执行ID（Slurm Job ID或Docker container ID）和partition不属于SOJ持久化业务ID。

省略docker字段的manifest选择Slurm；包含docker对象的manifest选择Docker，镜像及CPU/内存/时间限制随Job manifest保持不可变。docker不接受null或未知字段，镜像必须为预装的不可变摘要引用；精确字段和上下界只由manifest Schema定义。

固定脚本和路径、archive限制、凭据隔离、不可信用户程序与可信输出隔离、安全文件打开、原子生成结果、日志降级及后端终态映射均由[执行契约](../../judge.md)维护，本页不重复定义。
