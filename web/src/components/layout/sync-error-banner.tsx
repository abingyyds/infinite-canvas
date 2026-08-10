import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { onUserDataSyncError } from "@/services/user-data-sync";

export function SyncErrorBanner() {
    const { t } = useTranslation();
    const [message, setMessage] = useState("");

    useEffect(() => onUserDataSyncError(setMessage), []);

    if (!message) return null;
    return (
        <div role="alert" className="fixed inset-x-0 bottom-4 z-50 mx-auto flex w-fit max-w-[90vw] items-center gap-3 rounded-lg border border-red-300 bg-red-50 px-4 py-2 text-sm text-red-700 shadow-lg dark:border-red-900 dark:bg-red-950 dark:text-red-300">
            <span className="line-clamp-2">{t("common.syncFailed", { message })}</span>
            <button type="button" className="shrink-0 underline underline-offset-2" onClick={() => setMessage("")}>
                {t("common.dismiss")}
            </button>
        </div>
    );
}
