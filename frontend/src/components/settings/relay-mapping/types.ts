export interface ModelMappingEntry {
  clientModel: string;
  targetModel: string;
  targetProvider: string;
  targetGroupId?: string;
  expose: boolean;
  ownedBy?: string;
  injectChatTemplateKwargs?: boolean;
  multimodal?: boolean | null;
  variantEfforts?: string[];
  maxInputTokens?: number | null;
  /** 前端行渲染专用稳定 key(仅 UI 分页/vdom diff 用,保存时剔除,不落盘) */
  _rowKey?: string;
}

export interface PoolTabInfo {
  id: string;
  name: string;
  targetProvider: string;
  isCustom?: boolean;
}

export interface OtherGroupInfo {
  groupId: string;
  groupName: string;
  formats: string[];
  accountCount?: number;
  enabledCount?: number;
}
