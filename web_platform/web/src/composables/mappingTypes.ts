export interface ModelMappingEntry {
  clientModel: string;
  targetModel: string;
  targetProvider?: string;
  targetGroupId?: string; // For Other pool groups
  expose: boolean;
  multimodal?: boolean | null;
  ownedBy?: string;
  injectChatTemplateKwargs?: boolean;
  variantEfforts?: string[];
  candidateModels?: string[];
  useBenchmarkPool?: boolean;
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
  groupName?: string;
  formats?: string[];
  accountCount?: number;
  enabledCount?: number;
}
