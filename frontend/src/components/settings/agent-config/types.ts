// types.ts: Agent 配置可视化的公共类型定义。

export type FieldType =
  | 'string'
  | 'boolean'
  | 'number'
  | 'select'
  | 'model-select'
  | 'array'
  | 'kv-list'
  | 'object'
  | 'repeatable-object'
  | 'multiselect-variants';

export interface ConfigField {
  key: string;
  label: string;
  type: FieldType;
  description?: string;
  default?: any;
  options?: string[];
  placeholder?: string;
  toggleable?: boolean;
  secret?: boolean;
  children?: ConfigField[];
  // repeatable-object: 子项 key 名的 label (如"模型名")
  childKeyLabel?: string;
  // repeatable-object: 新增子项时的默认模板
  childTemplate?: { [key: string]: any };
  // multiselect-variants: 关联的嵌套 repeatable-object 字段 key (如 "variants"),
  // 勾选/取消勾选某候选等级时自动在该 RO 字段下添加/删除以候选名为 variantName 的子项。
  // 必填, 指向同 parent 下已有的 repeatable-object 子字段 key。
  targetRepeatableKey?: string;
  // model-select: 是否显示 1M 上下文声明开关（勾选时自动在模型名末尾添加 [1M] 后缀）
  with1mSuffix?: boolean;
  // select: 是否允许切换为自定义文本输入模式
  allowCustom?: boolean;
}

export interface ConfigSection {
  title: string;
  icon?: string;
  repeatable?: boolean;
  itemKeyField?: string;
  fields: ConfigField[];
  itemTemplate?: { [key: string]: any };
}

export interface AgentSchema {
  agentId: string;
  sections: ConfigSection[];
}

export interface AgentProfile {
  id: string;
  displayName: string;
  configPath: string;
  fileExists: boolean;
}
