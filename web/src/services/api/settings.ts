import { apiGet } from "@/services/api/request";

export type ModelCost = {
    model: string;
    credits: number;
};

export type PublicModelChannelSetting = {
    availableModels: string[];
    modelCosts: ModelCost[];
    defaultModel: string;
    defaultImageModel: string;
    defaultVideoModel: string;
    defaultTextModel: string;
    systemPrompt: string;
    allowCustomChannel: boolean | null;
};

export type PublicSettings = {
    modelChannel: PublicModelChannelSetting;
    auth: {
        allowRegister: boolean | null;
        linuxDo: { enabled: boolean };
    };
};

export async function fetchPublicSettings(token?: string) {
    return apiGet<PublicSettings>("/api/settings", undefined, token);
}
