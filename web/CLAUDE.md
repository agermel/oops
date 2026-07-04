# Frontend Conventions

## State Management

### TanStack Query（服务端状态）

所有从 API 获取的数据**必须**走 TanStack Query，不允许手动 `useState + fetch` 或手动 `setInterval` 轮询。

- Query keys 统一定义在 `src/hooks/queries.ts` → `queryKeys`
- 轮询用 `refetchInterval`，不要手写 `setInterval`
- `apiRequest<T>(url)` 自动抛 `ApiError`（含 `status`），`getErrorMessage(err, fallback)` 提取可读消息

```typescript
// 正确
const { data = [], isLoading, error } = useQuery<Item[]>({ queryKey, queryFn, refetchInterval: 30_000 });
// 错误 —— 不要这样写
const [data, setData] = useState([]); useEffect(() => { fetch().then(setData) }, []);
```

**已有的数据 hooks（按 domain 分文件）：**

| Hook | 文件 | 用途 |
|---|---|---|
| `useProjects` | `useProjects.ts` | 项目列表 |
| `useProjectServers(pid)` | `useServers.ts` | 项目下的服务器 |
| `useNodeletStatus()` | `useServers.ts` | Prober 健康状态（30s 轮询） |
| `useContainers(pid, nid)` | `useServers.ts` | 某服务器下的容器列表 |
| `useContainerDetail(pid, nid, cid)` | `useContainerDetail.ts` | 容器详情聚合 |
| `useMCPConnections()` | `useServers.ts` | MCP 连接列表（30s 轮询） |
| `useNodelets()` | `useNodelets.ts` | Nodelet 配置列表 |
| `useSkills()` | `useSkills.ts` | 技能列表 |
| `useSessions(pid?)` | `useSessions.ts` | 会话列表 |

### 本地状态 —— 用通用 hooks，不要手写样板

以下模式已被封装，**不要再手写原始 `useState` + 手动 setter**：

#### `useSet(initial?)` — 替代 `Set<string>` toggle

```typescript
// 错误 —— 不要手写
const [ids, setIds] = useState<Set<string>>(new Set());
const add = (id) => setIds(p => new Set(p).add(id));
// 正确
const ids = useSet();
ids.add("x"); ids.remove("x"); ids.toggle("x"); ids.has("x"); ids.clear();
```

`src/hooks/useSet.ts`

#### `useModal<T>(initialOpen?)` — 替代模态框 `showForm + editItem`

```typescript
// 错误 —— 不要手写
const [show, setShow] = useState(false);
const [editItem, setEditItem] = useState<Item | null>(null);
// 正确
const modal = useModal<Item>();
modal.onOpen();       // 新增模式（data=null）
modal.onOpen(item);   // 编辑模式（data=item）
modal.onClose();      // 关闭并清空
modal.open;           // boolean
modal.data;           // T | null
modal.setData(prev => ({ ...prev!, field: val })); // 表单内编辑
```

`src/hooks/useModal.ts`

#### `useToggle(initial?)` — 替代 `boolean` toggle

```typescript
const [on, { on, off, toggle, set }] = useToggle(false);
```

`src/hooks/useToggle.ts`

#### `useLogStream(pid, nid, cid)` — 替代手动 EventSource 管理

日志流生命周期完全封装：自动跟随 containerId 变化启停 EventSource，内置缓冲、节流刷新、自动重连、错误提示。

```typescript
const { logs, loading, error, autoScroll, setAutoScroll, panelRef, clear } = useLogStream(projectId, nodeletId, containerId);
// 当 containerId 变为空时自动关闭流；切换容器时自动关闭旧流打开新流
```

`src/hooks/useLogStream.ts`

## 组件模式

### App.tsx 的职责

**只是编排层**（orchestration），不是数据管理层。职责：
- 路由解析（usePathRouter）
- 调用数据 hooks 获取数据
- 按路由分支渲染不同的视图组件

App.tsx 中**不应再有**：
- 手动 `useState` 管理 API 数据（用 TanStack Query）
- `EventSource` / `setInterval` 生命周期管理（用对应 hook）
- 模态框 `showForm` / `editItem` 状态（用 `useModal`）

### Prop Drilling

如果某个组件需要透传 10+ 个 props 且大部分是原样转发给子组件，优先考虑：
1. 把数据获取移到实际消费的组件内部（TanStack Query 的缓存保证不会重复请求）
2. 而不是把所有 state 提升到 App.tsx 然后逐层透传

## 新增 Hook 的 Checklist

在创建新 hook 前，先检查 `src/hooks/` 目录下是否已有类似功能。如果没有：
- 文件名：`useXxx.ts`
- 放在 `src/hooks/` 下
- 如果涉及 API 数据获取，用 TanStack Query
- 如果是通用状态工具，保持单一职责
