import type { PublicSettings } from "@/services/api/settings";
import { createModelChannel, decodeChannelModel, encodeChannelModel, guessCapability, modelOptionsFromChannels, normalizeModelOptionValue, resolveModelChannel, useConfigStore, type AiConfig, type ModelChannel } from "@/stores/use-config-store";

export const GATEWAY_CHANNEL_ID = "gateway";

/** 该模型是否走内置网关（即请求经由本站后端转发，而非用户自配渠道直连）。 */
export function isGatewayModel(config: AiConfig, model: string) {
    return resolveModelChannel(config, model).id === GATEWAY_CHANNEL_ID;
}

/**
 * 后端 /api/v1 本身就是 OpenAI 兼容网关，登录后把它作为内置渠道注入渠道列表，
 * 图像/视频/文本/音频调用即自动带 JWT 走后端，无需改动各 api 模块。
 */
export function syncGatewayChannel(token: string, settings: PublicSettings | null) {
    useConfigStore.setState((state) => {
        const rest = state.config.channels.filter((channel) => channel.id !== GATEWAY_CHANNEL_ID);
        const models = settings?.modelChannel.availableModels || [];
        const gateway =
            token && models.length > 0
                ? createModelChannel({
                      id: GATEWAY_CHANNEL_ID,
                      name: "内置网关",
                      baseUrl: "/api",
                      apiKey: token,
                      apiFormat: "openai",
                      models: models.map((name) => ({ name, capability: guessCapability(name) })),
                  })
                : null;
        const channels: ModelChannel[] = gateway ? [gateway, ...rest] : rest;
        const config = {
            ...state.config,
            channels,
            models: modelOptionsFromChannels(channels),
            imageModel: pickModel(state.config.imageModel, channels, gateway, settings?.modelChannel.defaultImageModel),
            videoModel: pickModel(state.config.videoModel, channels, gateway, settings?.modelChannel.defaultVideoModel),
            textModel: pickModel(state.config.textModel, channels, gateway, settings?.modelChannel.defaultTextModel),
            model: pickModel(state.config.model, channels, gateway, settings?.modelChannel.defaultModel),
        };
        return { config };
    });
}

/**
 * 保留用户已配置好的渠道选择；选择失效、或仍指向没填 apiKey 的渠道时，
 * 回落到后台配置的默认模型，避免登录后仍指向不可用的自带渠道。
 */
function pickModel(current: string, channels: ModelChannel[], gateway: ModelChannel | null, fallback: string | undefined) {
    const normalized = normalizeModelOptionValue(current, channels);
    const decoded = normalized ? decodeChannelModel(normalized) : null;
    const usable = decoded ? channels.find((channel) => channel.id === decoded.channelId)?.apiKey.trim() : "";
    if (normalized && usable) return normalized;
    const name = (fallback || "").trim();
    if (!gateway || !name || !gateway.models.some((model) => model.name === name)) return normalized;
    return encodeChannelModel(gateway.id, name);
}
