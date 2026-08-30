import path from 'node:path';

import { defineConfig } from '@rsbuild/core';
import { pluginReact } from '@rsbuild/plugin-react';
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss';
import { TanStackRouterRspack } from '@tanstack/router-plugin/rspack';

// BACKEND_URL 是 rsbuild 构建期（Node 侧）变量，仅用于 dev devServer.proxy 转发目标，默认 http://127.0.0.1:8080。
// 它不是浏览器运行时变量（import.meta.env）；生产走 Nginx 同域反代，前端不硬编码后端地址。
const BACKEND = process.env.BACKEND_URL ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [pluginReact(), pluginTailwindcss()],
  source: {
    entry: {
      index: './src/main.tsx',
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  tools: {
    rspack: {
      plugins: [TanStackRouterRspack()],
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': { target: BACKEND, changeOrigin: true },
      '/health': { target: BACKEND, changeOrigin: true },
    },
  },
});
