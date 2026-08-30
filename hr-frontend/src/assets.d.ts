// 资源模块类型声明。
// 既成改动删除 env.d.ts 时，连带移除了 `/// <reference types="@rsbuild/core/types" />`，
// 该 reference 经 @rspack/core/module 提供 *.css 等资源模块声明。删除后
// `import '@/styles/globals.css'` 报 TS2882。此处仅补回必要的资源声明，
// 不恢复 import.meta.env（既成判定为无消费者）。
declare module '*.css';
