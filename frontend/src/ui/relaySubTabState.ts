/**
 * relaySubTabState.ts: 中继服务器设置页的子 Tab 激活状态(reactive)。
 *
 * 动机:「模型映射」子面板是设置页里最重的组件(每条映射一行组合框,条目可达数百),
 * 之前随设置页一打开就全量挂载渲染,即使用户只看「参数配置」等其他 Tab。
 * 现改为首次激活该子 Tab 后才挂载(ever-activated 语义,激活后保持挂载,
 * 与旧行为一致地保留未保存的行内编辑),设置页卸载时由 RelayPanel 复位归零。
 */
import { ref } from 'vue';

export const modelMappingEverActive = ref(false);
