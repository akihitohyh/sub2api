# Excel / BPS 会话代理（本地试验）

基线：`e39898c680ecd69381e549ae54c97011107f1757`（v2.8.14）。

账号编辑及批量编辑的 Excel / BPS 区域新增「BPS 会话固定 Mihomo 出口」，默认关闭。沿用账号 ChatGPT OAuth，不需要额外登录或协议 sidecar。启用后 BPS 请求优先使用内置 Mihomo；关闭此代理选项恢复账号原代理，关闭 Excel / BPS 协议恢复原 Codex 路由。未命中 BPS 模型范围的请求不受影响。

## 会话行为

- 复用现有客户端线程 / `session_id` / `prompt_cache_key` 解析，按账号、API key 和执行会话隔离绑定；缺少可识别会话时返回 400，不把所有匿名请求当成同一会话。
- 新会话分配到当前绑定会话数最少的合格节点。同一账号的不同会话可以使用不同节点；同一会话的多轮及并发请求保持原节点。
- 每个请求持有独立引用直到响应体关闭。仍有请求执行的绑定不回收；空闲 30 分钟后回收。最多 4096 个绑定，容量满返回 503，不驱逐活跃会话。
- 节点故障、禁用、国家过滤排除、订阅删除或节点配置变化时，原会话返回 503，不自动改绑、不回退直连。没有自动重放 BPS 请求。
- 绑定保存在进程内存，Sub2API 重启会重新分配；不承诺跨服务重启维持原 IP。
- 代理节点固定不等于公网 IP 固定。动态 IP 供应商必须提供粘性出口；本实现不改写供应商用户名或伪造 session 参数，也不验证供应商的实际出口稳定性。

## Mihomo 接入

每个节点配置身份使用独立、不可改派的本地 mixed listener，端口从 `127.0.0.1:19000` 分配，最多 4096 个。端口按节点而非会话分配，200 个会话不需要 200 个监听端口。节点配置身份改变会分配新端口；旧端口设置为 REJECT，避免连接池误连其他节点。节点历史过多超过上限时配置更新失败，需要维护窗口重启清理进程内历史。

监听入口由内置 Mihomo 配置生成，复用已有订阅、动态代理及国家过滤；不会改动打票 selector，也不占用打票 gate。打票标记为 used 的节点仍可承载业务，failed/disabled 节点不能分配。HTTP/2 长连接策略保持现有实现，连接池由不同节点端口隔离。

首次升级需要内置 Mihomo 重新加载配置，生成这些监听入口。配置更新沿用现有候选配置验证和回滚流程。没有新增数据库迁移。

## 本地验证

```bash
cd backend
go test -tags=unit ./internal/mihomo ./internal/service ./internal/repository -run 'BPS|Excel|Collection|Country|Scheduler.*Extra' -count=1
go test -race ./internal/mihomo -count=1
MIHOMO_INSTALL_SMOKE=1 go test ./internal/mihomo -run '^TestCollectionWithOfficialKernel$' -count=1
cd ../frontend
pnpm test:run src/components/account/__tests__/EditAccountModal.spec.ts src/components/account/__tests__/BulkEditAccountModal.spec.ts
pnpm build
```

内核测试仅下载固定版本官方内核，使用本地模拟代理出口，验证两个会话分别出站、同会话复用出口以及配置重载后绑定不变。200 并发覆盖的是离线会话分配和引用计数，不代表真实 BPS 200 并发或账号风控已验证。
