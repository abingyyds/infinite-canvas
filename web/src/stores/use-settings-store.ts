import { create } from "zustand";

import { fetchPublicSettings, type PublicSettings } from "@/services/api/settings";

type SettingsStore = {
    settings: PublicSettings | null;
    load: (token?: string) => Promise<void>;
};

export const useSettingsStore = create<SettingsStore>()((set) => ({
    settings: null,
    load: async (token) => {
        try {
            set({ settings: await fetchPublicSettings(token) });
        } catch {
            set({ settings: null });
        }
    },
}));
