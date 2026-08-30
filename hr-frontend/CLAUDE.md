# CLAUDE.md

本文件给在前端目录（hr-frontend/）下工作的 Claude Code 提供操作规约。项目整体定位、跨前后端约定、本地启动见上一层 [../CLAUDE.md](../CLAUDE.md)，本文件只承载前端独有约定，不重复后端内容。当前仓库处于 B 档可运行脚手架阶段，前端只有 account 登录链路端到端贯通并作为新增业务域的样板，其余业务域（config/dimension/assessment/questionbank/profile/dashboard/workspace）在导航里均为待激活占位，feature 尚未建立。规格权威源是 [../context/03_architecture/architecture.md](../context/03_architecture/architecture.md)，第 4 章承载运行时约定。

## 常用命令

包管理器是 pnpm（[.npmrc](.npmrc) 设 `shamefully-hoist=true` 把依赖扁平化到顶层，规避 hoist/peer 问题，安装前必跑 `pnpm install`）。

```bash
pnpm dev          # rsbuild dev，端口 3000，/api 与 /health 经 proxy 转发后端 8080
pnpm build        # 生产构建
pnpm preview      # 预览生产构建
pnpm type-check   # tsc --noEmit，唯一的静态检查门，改完代码必跑
pnpm test         # vitest run，跑一次测试
pnpm test:watch   # vitest watch 模式
```

仓库未配 ESLint/Prettier，没有 lint 脚本。测试用 [Vitest](vitest.config.ts)（jsdom 环境、globals、`@` alias 与 rsbuild 一致），全局 setup 在 [src/test/setup.ts](src/test/setup.ts)（注册 jest-dom 匹配器并 afterEach cleanup）。改完代码除 `pnpm type-check` 外，涉及逻辑改动还要跑 `pnpm test`。业务组件测试与各 feature 同目录或集中放 [src/test/](src/test/)。

新增 shadcn 组件用 `pnpm dlx shadcn@latest add <组件>`，CLI 按 [components.json](components.json) 的别名落盘到 [src/components/ui/](src/components/ui/)。

## 目录结构

[src/](src/) 各目录职责是既定约定，新增内容按归属落盘：

[routes/](src/routes/) 是 TanStack 文件路由的编排层，只做路由与页面入口，页面实现下沉到 feature。[features/](src/features/) 按业务域自包含，每个域三件套：`api.ts`（纯函数请求）、`hooks.ts`（TanStack Query 封装）、`types.ts`（域私有类型），目前只有 [features/account/](src/features/account/)。[lib/](src/lib/) 放框架级基础设施（http-client、query-client、contracts、jwt、utils），跨域共享。[stores/](src/stores/) 是 Zustand store，目前只有 [auth.ts](src/stores/auth.ts)。[components/ui/](src/components/ui/) 只放 shadcn new-york 原子件，[components/](src/components/) 放业务通用件（error-boundary、nav-menu、theme-provider）。[i18n/](src/i18n/) 是国际化配置与语言资源，[styles/](src/styles/) 只有 [globals.css](src/styles/globals.css) 一个文件。

跨域共享的契约类型（统一响应结构、`Account`、`LoginResult`、`MeResult`、`ErrCode` 等）集中收在 [lib/contracts.ts](src/lib/contracts.ts)，避免各 feature 的 types.ts 重复定义造成漂移。feature 私有类型才进 `features/<域>/types.ts`。

## 路由系统

路由由 `TanStackRouterRspack` 插件在 dev/build 时扫描 routes/ 自动生成 [routeTree.gen.ts](src/routeTree.gen.ts)（见 [rsbuild.config.ts](rsbuild.config.ts)）。该文件头部标 `@ts-nocheck` 并注明不可手改，新增路由只能在 routes/ 下加 `.tsx` 文件，文件内 `export const Route = createFileRoute('/路径')({...})`，保存即注册。

根路由 [__root.tsx](src/routes/__root.tsx) 下挂三棵子树：`_authenticated` 布局路由、`/login`、`/answer/$token`。后两者直接挂 root，刻意不套鉴权布局也不带顶栏。

`_authenticated` 下划线前缀是 pathless layout route，不贡献 URL 段，其子路由共享顶栏与 main 容器。鉴权在 [_authenticated/route.tsx](src/routes/_authenticated/route.tsx) 的 `beforeLoad` 里同步读 `useAuthStore.getState().token`，用 [lib/jwt.ts](src/lib/jwt.ts) 的 `isTokenExpired()` 本地校验过期，过期或缺失即 `throw redirect({ to: '/login' })`。这是同步本地探测，目的是避免带着过期 token 进受保护页再被 401 踢回造成登录态闪烁。该路由的 `component` 是 `AuthLayout`，渲染顶栏（品牌点、NavMenu、账号名、语言/主题/登出三个 ghost icon）与 `max-w-7xl` 的 main 容器。

[/answer/$token](src/routes/answer/$token.tsx) 是员工作答的一次性令牌页，`$token` 经 `Route.useParams()` 取参。它刻意挂在 root 而非 `_authenticated` 下，与主平台 JWT 体系物理隔离，用一次性令牌鉴权。这是有意的架构决定，不要把它挪进鉴权布局。

最后，[main.tsx](src/main.tsx) 在 `createRouter` 之后必须紧跟一段 `declare module '@tanstack/react-router' { interface Register { router: typeof router } }`，用于恢复 to/search/params 的字面量类型校验，删掉会丢失路由路径类型推导。

## 数据与状态层

axios [httpClient](src/lib/http-client.ts) 的 `baseURL` 是相对路径 `/api`，dev 走 rsbuild proxy、生产走 Nginx 同域反代，前端运行时不持有后端地址（[rsbuild.config.ts](rsbuild.config.ts) 里的 `BACKEND_URL` 是 Node 侧构建期变量，只作 dev proxy target，不是浏览器变量）。

响应拦截器按 `{code, message, data}` 解包：`code===0` 时把响应体替换为 `body.data`，消费方拿到的就是 payload 本身，写 `const { data } = await httpClient.post<X>(...)` 后 `data` 已是载荷，不要再多解一层；`code!==0` 抛 `ApiError(code, message)`。未授权做了一致化处理：HTTP 200 且 `code===1003` 与 HTTP 401 等价，都触发 [handleUnauthorized](src/lib/http-client.ts)（清 token、清 queryClient、跳 `/login`）。新增受保护接口无需自己处理 401，拦截器统一兜底。请求拦截器每次从 store 读 token 附 `Authorization: Bearer`。

`ApiError` 携带业务 code，消费方用 `instanceof ApiError` 分支。登录页只识别 `InvalidCredentials`(1001) 与通用错误两类，刻意不消费 `AccountDisabled`(1002)，这是与后端反枚举设计对齐的契约（账号不存在/密码错/禁用三条路径后端都返 1001）。

TanStack Query 的 hook 封装在 feature 的 `hooks.ts` 里，命名 `useXxx`，queryKey 用小写字面量数组（如 `['me']`）。换账号登录的 mutation 要在 `onSuccess` 里 `invalidateQueries` 废弃旧缓存，避免首页命中前账号数据；无 token 的 query 用 `enabled: !!token` 守卫。[queryClient](src/lib/query-client.ts) 全局配 `staleTime: 30s`、`gcTime: 5min`、`retry: 1`、`refetchOnWindowFocus: false`。两处登出（手动登出与 401 自动登出）都必须同时调 `queryClient.clear()`。

auth store（[stores/auth.ts](src/stores/auth.ts)）用 Zustand persist 落 localStorage，key 是硬编码字符串 `sili-smart-hr-auth`，改它会让现网已登录用户全部掉线。`partialize` 只持久化 `token` 与脱敏 `account`。跨模块非响应式读用 `useAuthStore.getState()`，组件内响应式读用选择器 `useAuthStore((s) => s.xxx)`。

## i18n

[i18n/config.ts](src/i18n/config.ts) 把中英资源同步 `import`，因此 i18n 在 `import '@/i18n/config'` 时即同步初始化完成，`useTranslation` 在路由外、ThemeProvider 挂载前都能用，不依赖 I18nextProvider。`fallbackLng: 'zh'`，`defaultNS: 'common'`，语言偏好存 localStorage key `sili-smart-hr-lang`。

命名空间有 common（默认）、auth、account、errorBoundary、notFound。页面级用 `useTranslation('域')` 指定 ns，跨 ns 引用传第二参数 `{ ns: 'common' }`。[zh.json](src/i18n/locales/zh.json) 与 [en.json](src/i18n/locales/en.json) 的 key 树必须逐一对齐同步维护，漏翻会让缺失 key 回退到 zh。

## 组件与样式

`components/ui/` 只放 shadcn 原子件（button/card/input/label/sonner），业务件绝不进 ui/；业务通用件放 `components/`，页面专用件随业务域生长到 `features/<域>/components/`。所有 class 合并经 [lib/utils.ts](src/lib/utils.ts) 的 `cn()`（twMerge + clsx）。

样式系统由 [DESIGN.md](DESIGN.md) 与 [src/styles/globals.css](src/styles/globals.css) 共同承载，二者关系是设计与实现：DESIGN.md 是设计系统的文档化描述（颜色/字体/圆角的字面量值与设计原则），globals.css 是 token 实现。冲突时以 globals.css 为准，它是运行时权威。

token 流向是：所有颜色、圆角、字体以 CSS 变量定义在 globals.css 的 `:root` 与 `.dark`，经 `@theme inline` 映射成 Tailwind 工具类，改一处全局生效。新增 token 要同时在 `:root` 与 `@theme inline` 各加一行，否则工具类不生效。Tailwind 4 走 CSS-first 配置（`@import "tailwindcss"` 加 `@theme` 块），没有 JS 配置文件；dark mode 用 `@custom-variant dark (&:is(.dark *))` class 策略，由 [theme-provider](src/components/theme-provider.tsx) 的 next-themes 给 `<html>` 加 `.dark` 切换。

全局滚动条样式在 [globals.css](src/styles/globals.css) 的 `@layer base` 统一收口，任何可滚动容器（弹窗、表格、下拉、textarea 等）自动继承，不在组件内单独写。规格是 thin/overlay 风格：轨道透明，滑块用 `color-mix(in oklch, var(--muted-foreground) 35%, transparent)`，hover 提到 55%，宽高 8px，圆角跟 `--radius`。覆盖 webkit（`::-webkit-scrollbar*`）与 Firefox（`scrollbar-width`/`scrollbar-color`）两套引擎，色相走 token 深色模式自动跟随。新增可滚动区域直接 overflow 即可，无需关心滚动条外观。

写组件前必读 [DESIGN.md](DESIGN.md)，几条硬约定：近黑 primary 作统一主色，承载主操作与选中态；粉彩 block-* 仅作点缀（登录页 hero、空状态、个别 highlight 卡片），绝不作后台页面或表格底色；圆角后台按钮/输入框用 md（6px），卡片用 xl（12px），只有 icon 按钮和登录页 CTA 才用 full，后台没有 pill 文字按钮；分层靠 1px hairline 边线加 shadow-xs，不用 shadow-md 以上；Inter Variable 是唯一 sans，JetBrains Mono Variable 仅作 eyebrow/caption（大写带正字距）不进正文；后台正文默认 text-sm（14px），主标题 24px，36px 以上大字号是登录页 hero 专属；颜色一律走 token，不写死 hex。

弹窗内容溢出由原子件兜底：[dialog.tsx](src/components/ui/dialog.tsx) 的 `DialogContent` 与 [alert-dialog.tsx](src/components/ui/alert-dialog.tsx) 的 `AlertDialogContent` 都带 `max-h-[calc(100dvh-2rem)] overflow-y-auto`，内容超高时弹窗内部整体可上下滚动，不会撑出视口裁掉关闭按钮和 footer。新增弹窗直接用这两个原子件即可，不要在调用方自己加 `max-h`/`overflow`；短内容不会触发滚动条，无副作用。若后续出现长列表类弹窗需要 header/footer 在滚动时钉住，再单独约定三段式结构，不要回头改这两个原子件。

## account 样板（新增业务域照此七步走）

account 登录链路是端到端贯通的唯一样板，新增业务域照它走：先在 [lib/contracts.ts](src/lib/contracts.ts) 定义跨域共享类型，再到 feature 的 `api.ts` 写纯函数请求（拦截器已解包，直接解构返回），`hooks.ts` 包成 TanStack Query hook（mutation 在 onSuccess 废弃相关缓存，query 用 enabled 守卫），route 组件用 react-hook-form 加 zodResolver，schema 内用 `t()` 构造校验消息保证校验文案也国际化，`onSubmit` 里 mutate，成功走 setAuth 与 toast 与 navigate，失败用 `instanceof ApiError` 分支，最后 store 集成与 `_authenticated` 布局挂载。[routes/login.tsx](src/routes/login.tsx)、[features/account/](src/features/account/) 是完整参照。NavMenu 里已预留各业务域编号（见 [components/nav-menu.tsx](src/components/nav-menu.tsx)）。

## TS 与构建约束

[tsconfig.json](tsconfig.json) 开了 `strict`、`noUnusedLocals`、`noUnusedParameters`、`verbatimModuleSyntax`。后两者尤其要紧：未用的 import、变量、参数会让 type-check 失败；类型必须用 `import type`，混用会报错（代码里严格区分如 `import type { AxiosResponse }`）。路径别名 `@/*` 指 `./src/*`，与 rsbuild 别名一致。

[src/assets.d.ts](src/assets.d.ts) 只声明了 `*.css` 模块。项目刻意不恢复 `import.meta.env`，运行时环境判断走 `process.env.NODE_ENV`（rsbuild 在客户端静态替换它）。src 下没有自定义 index.html，Rsbuild 用默认模板。

## 陷阱

[recharts](package.json)、[@tanstack/react-table](package.json)、dayjs 在 package.json 里声明但当前零 import，是为业务域预留的前瞻性依赖，骨架阶段未用。[next-themes](package.json)、sonner、lucide-react 是真实在用的。注意 [@base-ui/react](src/components/nav-menu.tsx) 尚未引入，只在 nav-menu 注释里作为后续 hover 下拉的候选方案被提及，根级文档把它列为在用组件与实现有偏差，落地时如需再用再装。

Windows 环境下 Rsbuild dev 可能绑到 IPv6 `::1`，localhost 访问不到时改用 127.0.0.1，rsbuild 的 proxy target 也用 `127.0.0.1:8080` 与此呼应。系统代理（常见 7897 端口）会劫持 pnpm install 与 go mod，拉取卡住先关代理或配镜像加速。

routeTree.gen.ts 手改会在下次 dev/build 丢失，只能加 routes/ 文件。persist key（`sili-smart-hr-auth`）与语言 key（`sili-smart-hr-lang`）是硬编码 localStorage key，改动会让现网用户掉线或丢语言偏好。
