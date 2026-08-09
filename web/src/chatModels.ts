export const defaultChatModelId = 'sonnet';

export type ChatModel = {
  id: string;
  provider: string;
  model: string;
  supportsBirdyTools: boolean;
  available: boolean;
  unavailableReason?: string;
};

export const fallbackChatModels: ChatModel[] = [
  {
    id: defaultChatModelId,
    provider: 'Claude Code',
    model: 'Sonnet',
    supportsBirdyTools: true,
    available: true,
  },
];

export function normalizeStoredModelId(value: unknown): string {
  return typeof value === 'string' && value.trim() ? value.trim() : defaultChatModelId;
}

export function modelsWithSavedSelection(models: ChatModel[], selectedId: string): ChatModel[] {
  if (models.some(model => model.id === selectedId)) return models;
  return [{
    id: selectedId,
    provider: 'Saved model',
    model: selectedId,
    supportsBirdyTools: false,
    available: false,
    unavailableReason: 'This saved model is no longer registered. Choose an available model.',
  }, ...models];
}

export function createQueuedChat(convId: string, prompt: string, modelId: string) {
  return { convId, prompt, modelId };
}

export function newConversationModelId(catalogDefaultId?: string): string {
  return catalogDefaultId || defaultChatModelId;
}

export function effectiveConversationModelId(activeModelId: string | undefined, catalogDefaultId: string): string {
  return activeModelId?.trim() || newConversationModelId(catalogDefaultId);
}

export function parseChatModelCatalog(value: unknown): { defaultId: string; models: ChatModel[] } {
  if (!value || typeof value !== 'object') throw new Error('invalid model catalog');
  const record = value as Record<string, unknown>;
  if (record.ok !== true || typeof record.default !== 'string' || !Array.isArray(record.models)) {
    throw new Error('invalid model catalog');
  }
  if (record.models.length === 0 || record.models.length > 12) throw new Error('invalid model catalog');

  const seen = new Set<string>();
  const models = record.models.map((entry): ChatModel => {
    if (!entry || typeof entry !== 'object') throw new Error('invalid model entry');
    const model = entry as Record<string, unknown>;
    for (const field of ['id', 'provider', 'model']) {
      if (typeof model[field] !== 'string' || !(model[field] as string).trim() || (model[field] as string).length > 120) {
        throw new Error('invalid model entry');
      }
    }
    if (typeof model.available !== 'boolean' || typeof model.supports_birdy_tools !== 'boolean') {
      throw new Error('invalid model entry');
    }
    const id = (model.id as string).trim();
    if (seen.has(id)) throw new Error('duplicate model id');
    seen.add(id);
    const unavailableReason = typeof model.unavailable_reason === 'string'
      ? model.unavailable_reason.trim().slice(0, 160)
      : undefined;
    return {
      id,
      provider: (model.provider as string).trim(),
      model: (model.model as string).trim(),
      supportsBirdyTools: model.supports_birdy_tools,
      available: model.available,
      unavailableReason,
    };
  });
  const configuredDefault = models.find(model => model.id === record.default);
  if (!configuredDefault) throw new Error('invalid default model');
  if (configuredDefault.available && configuredDefault.supportsBirdyTools) {
    return { defaultId: configuredDefault.id, models };
  }
  if (!seen.has(defaultChatModelId)) throw new Error('unavailable default model');
  return { defaultId: defaultChatModelId, models };
}

export function createChatRequest(prompt: string, modelId: string) {
  return { prompt, model: modelId };
}

export function modelDisplayName(model: ChatModel) {
  return `${model.provider} · ${model.model}`;
}
