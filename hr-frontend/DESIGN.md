---
version: 1.0
name: sili-smart-hr-frontend-design
description: "sili-smart-hr 前端设计系统（后台化落地版）。以编辑克制为审美内核，映射到后台密度：近黑 primary 作统一主色，Inter Variable 加 JetBrains Mono，圆角收敛，语义色补齐，粉彩色块仅作登录页与点缀。token 权威来源为 src/styles/globals.css。"

colors:
  primary: "oklch(0.205 0 0)"
  on-primary: "oklch(0.985 0 0)"
  ink: "oklch(0.18 0 0)"
  canvas: "oklch(1 0 0)"
  surface-soft: "oklch(0.97 0.002 95)"
  hairline: "oklch(0.92 0 0)"
  muted-ink: "oklch(0.5 0 0)"
  accent: "oklch(0.96 0 0)"
  ring: "oklch(0.205 0 0)"
  destructive: "oklch(0.577 0.245 27.325)"
  on-destructive: "oklch(0.985 0 0)"
  success: "oklch(0.62 0.17 145)"
  warning: "oklch(0.78 0.14 75)"
  info: "oklch(0.6 0.1 240)"
  block-lime: "#dceeb1"
  block-lilac: "#c5b0f4"
  block-cream: "#f4ecd6"
  block-mint: "#c8e6cd"
  block-pink: "#efd4d4"
  block-coral: "#f3c9b6"
  block-navy: "#1f1d3d"

typography:
  font-sans: "Inter Variable, ui-sans-serif, system-ui, sans-serif"
  font-mono: "JetBrains Mono Variable, ui-monospace, monospace"
  hero-title:
    fontFamily: "{typography.font-sans}"
    fontSize: "36–48px (text-4xl md:text-5xl)"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: tight
    use: "仅登录页 hero"
  page-title:
    fontFamily: "{typography.font-sans}"
    fontSize: 24px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: tight
    use: "后台页面主标题"
  card-title:
    fontFamily: "{typography.font-sans}"
    fontSize: 18px
    fontWeight: 600
    use: "卡片、区块标题"
  body:
    fontFamily: "{typography.font-sans}"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.5
    use: "后台主体正文（text-sm）"
  body-lg:
    fontFamily: "{typography.font-sans}"
    fontSize: 16px
    fontWeight: 400
    use: "正文大号（text-base）"
  eyebrow:
    fontFamily: "{typography.font-mono}"
    fontSize: 12px
    fontWeight: 400
    letterSpacing: 0.18em
    textTransform: uppercase
    use: "品牌标记、分类标签"

rounded:
  sm: "4px (calc(0.5rem - 4px))"
  md: "6px (calc(0.5rem - 2px))"
  lg: "8px (0.5rem)"
  xl: "12px (calc(0.5rem + 4px))"
  full: "9999px"

spacing: "Tailwind 4 默认 scale（4px 基数）；容器 max-w-7xl 居中，顶栏 h-14，内容区 px-4 py-6"

components:
  button:
    base: "rounded-md，shadow-xs，h-9，font-medium"
    variants: "default(primary) / secondary / outline / ghost / destructive / link"
  button-cta:
    extends: "{components.button}"
    rounded: "{rounded.full}"
    use: "仅登录页主操作，后台不用 pill 文字按钮"
  text-input:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    border: "{colors.hairline}"
    shadow: "shadow-xs"
    height: "h-9"
  card:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    rounded: "{rounded.xl}"
    border: "{colors.hairline}"
    shadow: "shadow-xs"
  top-nav:
    backgroundColor: "{colors.canvas} / 80% + backdrop-blur"
    textColor: "{colors.ink}"
    height: "56px (h-14)"
    position: "sticky top-0"
    brand-mark: "{colors.block-lilac} 圆点 + appTitle"
  login-hero:
    backgroundColor: "{colors.block-lilac}"
    textColor: "{colors.ink}"
    rounded: "无（全屏分栏）"
    typography: "{typography.hero-title} + {typography.eyebrow}"
  color-block-accent:
    backgroundColor: "{colors.block-*} 之一"
    use: "空状态插画、个别 highlight 卡片，不作后台页面底色"

---

## 概述

sili-smart-hr 是综合人才测评平台，前端是企业内部后台工具，用户每天登记录数据、查台账、配模型、看画像。设计第一原则是信息密度和操作效率，不是叙事冲击。

审美内核取自编辑设计的克制语言：单色骨架、用 font-weight 而非灰度透明度表达层级、shadow-light 用边线分层、token 严格收束。早期曾参考 Figma 官网营销页的逆向稿作为审美来源，但其色块叙事、86px 大标题、pill 唯一按钮、私有 figmaSans 等营销页语言均未照搬，已后台化改造。粉彩色块保留为点缀，仅出现在登录页与个别空状态。

token 权威来源是 [src/styles/globals.css](src/styles/globals.css)。所有颜色、圆角、字体经 CSS 变量定义，在 `@theme inline` 映射为工具类，改一处全局生效，组件层基本无需改动。

**核心特征：**
- 近黑 primary（{colors.primary}）作统一主色，承载主操作、选中态、聚焦环。Linear、Vercel 同路。
- Inter Variable 承担全部 sans 场景，JetBrains Mono Variable 仅用于 eyebrow 和 caption 类标签，始终大写带正字距。
- 圆角收敛：后台主体 md（6px），卡片 xl（12px），圆形 icon 与登录页 CTA 才用 full。没有 50px pill 文字按钮。
- 粉彩 block-* 是点缀词汇，不是底色词汇。后台页面始终保持白底单色。
- shadow-light：靠 1px hairline 边线和 shadow-xs 分层，不用重阴影，不靠阴影制造焦点。

## 颜色

### 主色与文字
- **primary**（{colors.primary}）：近黑，系统主色。主操作按钮、选中态、聚焦环、顶栏品牌点。
- **on-primary**（{colors.on-primary}）：白，primary 上的文字。
- **ink**（{colors.ink}）：近黑，所有标题与正文文字。后台 hierarchy 主要靠 font-weight，不靠灰度。
- **canvas**（{colors.canvas}）：纯白，默认页面背景与卡片底。
- **muted-ink**（{colors.muted-ink}）：收敛灰，次要文字（说明、占位、表头辅助）。后台保留灰阶是密度妥协，编辑原稿主张纯 weight 层级在此让步。
- **surface-soft**（{colors.surface-soft}）：略暖白，次要容器底（次级按钮、icon 按钮底）。

### 边线与交互
- **hairline**（{colors.hairline}）：1px 边线，卡片、输入框、表格分隔。
- **accent**（{colors.accent}）：hover 态浅底，导航项悬浮、ghost 按钮悬浮。
- **ring**（{colors.ring}）：聚焦环，取 primary 近黑，让聚焦带品牌感。

### 语义色（后台刚需）
- **destructive**（{colors.destructive}）+ **on-destructive**（{colors.on-destructive}）：危险操作与错误提示，文字色随 token 不写死。
- **success**（{colors.success}）：成功态、启用标记、正向指标。
- **warning**（{colors.warning}）：警告、待处理。
- **info**（{colors.info}）：提示、中性徽章。

### 粉彩点缀（不作底色）
- **block-lime / lilac / cream / mint / pink / coral / navy**：取自早期 Figma 营销页参照稿。仅用于登录页 hero、空状态插画、个别 highlight 卡片。后台任何数据密集界面都不用粉彩铺底。

### 深色模式
`.dark` 下 primary 反转为白、背景转近黑，语义色提亮以保证对比，粉彩在深色下保持（主要用在登录页，深色下亦可）。深色态经 next-themes 的 `.dark` class 切换，Toaster 等组件自动跟随。

## 字体

### 字族
- **Inter Variable**：唯一 sans，承担标题、正文、按钮、表单全部文字。可变字重轴用于表达层级。
- **JetBrains Mono Variable**：唯一 mono，仅用于 eyebrow 和 caption 类标签，始终大写带 0.18em 正字距，是分类工具不是阅读字体。

两者经 `@fontsource-variable` 引入，在 `@theme inline` 注册为 `--font-sans` 与 `--font-mono`。body 默认 `font-sans antialiased` 并开启 OpenType kern。

### 层级

| 角色 | 字号 | 字重 | 用途 |
|---|---|---|---|
| hero-title | 36–48px | 600 | 仅登录页 hero |
| page-title | 24px | 600 | 后台页面主标题 |
| card-title | 18px | 600 | 卡片、区块标题 |
| body-lg | 16px | 400 | 正文大号 |
| body | 14px | 400 | 后台主体正文、表格、表单 |
| eyebrow | 12px | 400 mono | 品牌标记、分类标签，大写带正字距 |

### 原则
- 后台主体正文用 14px（text-sm），信息密度优先。只有登录页 hero 放大到 36–48px。
- 层级靠 font-weight 与字号双轴，次要文字才动用 muted-ink 灰。
- mono 是分类工具，不进正文。

## 布局

### 容器约定
- 内容容器 `max-w-7xl`（1280px）居中，左右 `px-4`。
- 顶栏 `h-14`（56px），sticky 吸附顶部，半透明白底加 backdrop-blur。
- 内容区 `main` 与顶栏同宽对齐，垂直 `py-6`。

### 顶栏（top-nav）
sticky 白底毛玻璃，左侧粉彩圆点加 appTitle 作品牌标记，中部 NavMenu（一级平铺、二级 hover 下拉），右侧账号名加语言、主题、登出三个 ghost icon。待激活的导航项弱化显示。

### 登录页（login-hero）
全屏左右分栏：左侧 lilac 粉彩 hero 放品牌 eyebrow、hero-title 与 tagline；右侧白底表单配 pill 主 CTA。移动端折叠为单列，hero 在上表单在下。这是全系统唯一大方使用粉彩底与大字号的地方。

### 留白
后台留白服务于扫读效率，不追求营销页的呼吸感。卡片内 `gap-6`、`px-6`，区块间靠 main 的 `py-6` 与卡片自身边线分隔。

## 层次与阴影

| 层级 | 处理 | 用途 |
|---|---|---|
| 0 平坦 | 无阴影无边线 | 粉彩 hero、顶栏背景 |
| 1 边线 | 1px hairline | 卡片、输入框、表格分隔 |
| 2 轻起 | shadow-xs | 卡片、按钮、输入框悬浮感 |

shadow-light 原则：能用 hairline 边线分层就不用阴影。card 用 border 加 shadow-xs，不用 shadow-md 以上。粉彩块本身就是层次装置，绝不再叠阴影。

## 形状

| token | 值 | 用途 |
|---|---|---|
| sm | 4px | 小 chip |
| md | 6px | 按钮、输入框主体 |
| lg | 8px | 列表项、图像框 |
| xl | 12px | 卡片 |
| full | 9999px | icon 按钮、登录页 pill CTA、圆点 |

后台没有 50px pill 文字按钮。pill 只在登录页主操作出现。

## 组件

### 按钮（button）
shadcn new-york cva，rounded-md，shadow-xs，h-9。六个 variant：default（primary 近黑）、secondary、outline、ghost、destructive、link。destructive 文字色用 on-destructive 随 token，不写死。登录页主 CTA 在此基础上加 rounded-full。

### 输入框（text-input）
rounded-md，hairline 边线，shadow-xs，h-9，bg-transparent 透出 canvas。聚焦态靠 ring 不靠填色变化。

### 卡片（card）
rounded-xl（12px），hairline 边线，shadow-xs，`gap-6` 内距。后台信息容器的主力。

### 顶栏（top-nav）
见布局章节。sticky 毛玻璃，粉彩圆点品牌标记。

### 登录 hero（login-hero）
见布局章节。粉彩分栏叙事的唯一落点。

### 粉彩点缀（color-block-accent）
空状态插画、个别 highlight 卡片可选用一个 block-* 色，单视口不超过一处。

## 应该做

- primary 近黑只用于真正的主操作和选中态，稀缺使用。
- 新页面默认正文 text-sm（14px），主标题 page-title（24px），靠 font-weight 拉层级。
- 字体只用 Inter 与 JetBrains Mono，mono 仅限 eyebrow/caption 大写带正字距。
- 圆角后台 md、卡片 xl，icon 与登录页 CTA 才 full。
- 后台页面保持白底单色，靠 hairline 边线与 shadow-xs 分层。
- 粉彩 block-* 只在登录页、空状态、个别 highlight 出现。

## 不应该做

- 不把粉彩当后台页面或表格底色，数据密集界面会刺眼。
- 不把 36px 以上大字号搬进后台，那是登录页 hero 的专属。
- 不全系统 pill 按钮，后台保持 md 圆角。
- 不绕过 token 写死颜色或 hex，所有色走 CSS 变量。
- 不引入 figmaSans/figmaMono 等私有字体，已用 Inter/JetBrains Mono 替代。
- 不引入粉彩与语义色之外的新强饱和品牌色。

## 实施约束

- token 权威在 [src/styles/globals.css](src/styles/globals.css)，改 `:root` 与 `.dark` 全局生效，`@theme inline` 自动桥接到工具类。
- 组件优先复用 shadcn new-york 现有件，靠 token 出个性，不重写组件结构。
- 新增业务页面在 `_authenticated` 顶栏与 main 容器约定内生长，挂载点见 [src/routes/_authenticated/route.tsx](src/routes/_authenticated/route.tsx)。
- 所有可见文案走 i18next，zh.json 与 en.json 同步维护，不硬编码可见文字。
- 改 `--radius` 一处即可全局影响圆角，派生 sm/md/lg/xl 自动跟随。
