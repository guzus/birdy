import { expect, test } from 'bun:test';
import {
  createChatRequest,
  createQueuedChat,
  defaultChatModelId,
  effectiveConversationModelId,
  modelDisplayName,
  modelsWithSavedSelection,
  newConversationModelId,
  normalizeStoredModelId,
  parseChatModelCatalog,
} from './chatModels';

const catalog = {
  ok: true,
  default: 'sonnet',
  models: [
    { id: 'sonnet', provider: 'Claude Code', model: 'Sonnet', supports_birdy_tools: true, available: true },
    { id: 'deepseek-flash', provider: 'OpenCode Go', model: 'DeepSeek V4 Flash', supports_birdy_tools: true, available: false, unavailable_reason: 'Host not configured' },
  ],
};

test('parses ordered model metadata and preserves unavailable choices', () => {
  const parsed = parseChatModelCatalog(catalog);
  expect(parsed.defaultId).toBe('sonnet');
  expect(parsed.models.map(model => model.id)).toEqual(['sonnet', 'deepseek-flash']);
  expect(parsed.models[1].available).toBe(false);
  expect(modelDisplayName(parsed.models[1])).toBe('OpenCode Go · DeepSeek V4 Flash');
});

test('rejects duplicate, malformed, and unregistered defaults', () => {
  expect(() => parseChatModelCatalog({ ...catalog, default: 'missing' })).toThrow();
  expect(() => parseChatModelCatalog({ ...catalog, models: [catalog.models[0], catalog.models[0]] })).toThrow();
  expect(() => parseChatModelCatalog({ ...catalog, models: [{ id: 'x', available: true }] })).toThrow();
});

test('creates the exact bounded API request without client provider fields', () => {
  expect(createChatRequest('hello', 'deepseek-flash')).toEqual({ prompt: 'hello', model: 'deepseek-flash' });
});

test('migrates legacy conversations and keeps removed saved models explicit', () => {
  expect(normalizeStoredModelId(undefined)).toBe(defaultChatModelId);
  expect(normalizeStoredModelId(' deepseek-flash ')).toBe('deepseek-flash');
  const models = modelsWithSavedSelection(parseChatModelCatalog(catalog).models, 'retired-model');
  expect(models[0]).toEqual({
    id: 'retired-model',
    provider: 'Saved model',
    model: 'retired-model',
    supportsBirdyTools: false,
    available: false,
    unavailableReason: 'This saved model is no longer registered. Choose an available model.',
  });
});

test('queued chat captures the model selected when the prompt was queued', () => {
  expect(createQueuedChat('conv-1', 'next', 'deepseek-flash')).toEqual({
    convId: 'conv-1', prompt: 'next', modelId: 'deepseek-flash',
  });
});

test('uses the validated server default only for chats created after catalog load', () => {
  expect(newConversationModelId()).toBe('sonnet');
  const loaded = parseChatModelCatalog({ ...catalog, default: 'deepseek-flash', models: [
    catalog.models[0],
    { ...catalog.models[1], available: true },
  ] });
  expect(newConversationModelId(loaded.defaultId)).toBe('deepseek-flash');
  expect(normalizeStoredModelId(undefined)).toBe('sonnet');
});

test('binds the first send, scan, and deep dive to the new conversation default', () => {
  const defaultId = 'deepseek-flash';
  for (const operation of ['send', 'scan', 'deep-dive']) {
    const modelId = effectiveConversationModelId(undefined, defaultId);
    expect(createChatRequest(operation, modelId)).toEqual({ prompt: operation, model: defaultId });
  }
  expect(effectiveConversationModelId('sonnet', defaultId)).toBe('sonnet');
});

test('falls back deterministically when the configured default is unavailable', () => {
  const parsed = parseChatModelCatalog({ ...catalog, default: 'deepseek-flash' });
  expect(parsed.defaultId).toBe('sonnet');
  expect(() => parseChatModelCatalog({
    ...catalog,
    default: 'deepseek-flash',
    models: [catalog.models[1]],
  })).toThrow();
});
