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
