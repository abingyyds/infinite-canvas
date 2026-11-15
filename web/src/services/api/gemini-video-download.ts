import i18n from "@/i18n";
import { useUserStore } from "@/stores/use-user-store";
import { withLocalProxy, type AiConfig } from "@/stores/use-config-store";

export async function downloadGeminiVideo(uri: string, config: Pick<AiConfig, "baseUrl" | "apiKey">, signal?: AbortSignal): Promise<Blob> {
    const failure = () => new Error(i18n.t("apiErrors.videoDownloadFailed"));
    const provider = new URL(config.baseUrl);
    const target = new URL(uri, provider);
    if (!["https:", "http:"].includes(target.protocol) || target.username || target.password) throw failure();

    const sameProvider = target.origin === provider.origin;
    const apiKey = sameProvider ? config.apiKey : "";
    if (sameProvider) target.searchParams.delete("key");
    const url = target.href;

    // A provider redirect may lead to signed storage. Never follow it in the
    // browser with the provider key attached.
    try {
        const response = await fetch(withLocalProxy(url), {
            headers: apiKey ? { "x-goog-api-key": apiKey } : undefined,
            redirect: apiKey ? "error" : "follow",
            signal,
        });
        if (response.ok) return await response.blob();
    } catch (error) {
        if (signal?.aborted) throw error;
    }

    const token = useUserStore.getState().token;
    if (!token) throw failure();
    const response = await fetch("/api/media-content", {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ url, ...(apiKey ? { apiKey, credentialType: "gemini" } : {}) }),
        signal,
    });
    if (!response.ok) throw failure();
    return await response.blob();
}
