// 开发辅助：CLI 之外手动再生 routeTree.gen.ts（router-generator 是 devDep 可直接调）。
// 用法：node scripts/regen-route-tree.mjs（新增/删除路由文件后跑一次）。
import { Generator, getConfig } from '@tanstack/router-generator';

const config = getConfig({ target: 'react' }, process.cwd());
const generator = new Generator({ config, root: process.cwd() });
await generator.run();
console.log('routeTree.gen.ts regenerated');
