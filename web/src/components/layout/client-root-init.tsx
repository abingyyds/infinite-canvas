import type { ReactNode } from "react";
import { useEffect } from "react";
import { App } from "antd";
import { useTranslation } from "react-i18next";

import { takeChannelImport } from "@/lib/channel-import";
import { useConfigStore } from "@/stores/use-config-store";
import { usePromptSourceScheduler } from "@/hooks/use-prompt-source-scheduler";

export function ClientRootInit({ children }: { children: ReactNode }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const importChannelCredentials = useConfigStore((state) => state.importChannelCredentials);
    const openConfigDialog = useConfigStore((state) => state.openConfigDialog);

    usePromptSourceScheduler();

    useEffect(() => {
        const imported = takeChannelImport();
        if (!imported) return;
        openConfigDialog(false, "channels");
        if (imported.discardedKey) message.info(t("config.pasteImportedKey"));
        if (!imported.baseUrl) return;
        const result = importChannelCredentials({ baseUrl: imported.baseUrl });
        if (result.status === "created") message.success(t("config.importedChannelCreated", { name: result.channelName }));
        else if (result.status === "updated") message.success(t("config.importedChannelUpdated", { name: result.channelName }));
        else if (result.status === "missing-base-url") message.error(t("config.importedChannelBaseUrlRequired"));
        else message.error(t("config.importedChannelBaseUrlInvalid"));
    }, [importChannelCredentials, message, openConfigDialog, t]);

    return <>{children}</>;
}
