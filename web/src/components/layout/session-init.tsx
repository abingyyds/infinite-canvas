import axios from "axios";
import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { syncGatewayChannel } from "@/services/gateway-channel";
import { startUserDataSync, stopUserDataSync } from "@/services/user-data-sync";
import { useSettingsStore } from "@/stores/use-settings-store";
import { useUserStore } from "@/stores/use-user-store";

export function SessionInit() {
    const hydrateUser = useUserStore((state) => state.hydrateUser);
    const clearSession = useUserStore((state) => state.clearSession);
    const token = useUserStore((state) => state.token);
    const user = useUserStore((state) => state.user);
    const isReady = useUserStore((state) => state.isReady);
    const settings = useSettingsStore((state) => state.settings);
    const loadSettings = useSettingsStore((state) => state.load);
    const navigate = useNavigate();
    const location = useLocation();

    useEffect(() => {
        void hydrateUser();
    }, [hydrateUser]);

    // 后端返回 401 说明会话已失效，清理本地会话并跳登录；只认自家 /api，
    // 用户自配渠道返回的 401 属于对方的鉴权失败，不能拿来登出。
    useEffect(() => {
        const onExpired = (config: { url?: string } | undefined, status: number | undefined) => {
            if (status !== 401 || !config?.url?.startsWith("/api")) return;
            if (!useUserStore.getState().token) return;
            clearSession();
            stopUserDataSync();
            navigate(`/login?redirect=${encodeURIComponent(location.pathname + location.search)}`, { replace: true });
        };
        const interceptor = axios.interceptors.response.use(
            (response) => {
                onExpired(response.config, response.status);
                return response;
            },
            (error) => {
                onExpired(error?.config, error?.response?.status);
                return Promise.reject(error);
            },
        );
        return () => axios.interceptors.response.eject(interceptor);
    }, [clearSession, location.pathname, location.search, navigate]);

    useEffect(() => {
        if (!isReady) return;
        void loadSettings(token || undefined);
    }, [isReady, loadSettings, token]);

    useEffect(() => {
        if (!isReady) return;
        syncGatewayChannel(token, settings);
    }, [isReady, settings, token]);

    useEffect(() => {
        if (!isReady) return;
        if (token && user) {
            void startUserDataSync(token, user.id);
            return;
        }
        stopUserDataSync();
    }, [isReady, token, user]);

    return null;
}
