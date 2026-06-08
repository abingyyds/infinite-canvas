"use client";

import { AUTH_TOKEN_KEY } from "@/services/api/auth";

export function currentUserScope() {
    if (typeof window === "undefined") return "guest";
    const token = readStoredToken();
    if (!token) return "guest";
    const payload = decodeJwtPayload(token);
    const userId = stringField(payload?.userId) || stringField(payload?.sub);
    return safeScope(userId || "guest");
}

export function scopedStoreKey(key: string) {
    return `${key}:${currentUserScope()}`;
}

function readStoredToken() {
    const candidates = [window.localStorage.getItem(AUTH_TOKEN_KEY), window.localStorage.getItem(`zustand:${AUTH_TOKEN_KEY}`)];
    for (const item of candidates) {
        const token = tokenFromPersistedState(item);
        if (token) return token;
    }
    return "";
}

function tokenFromPersistedState(value: string | null) {
    if (!value) return "";
    try {
        const parsed = JSON.parse(value);
        return stringField(parsed?.state?.token) || stringField(parsed?.token);
    } catch {
        return "";
    }
}

function decodeJwtPayload(token: string) {
    const parts = token.split(".");
    if (parts.length < 2) return null;
    try {
        return JSON.parse(atob(parts[1].replace(/-/g, "+").replace(/_/g, "/")));
    } catch {
        return null;
    }
}

function stringField(value: unknown) {
    return typeof value === "string" ? value.trim() : "";
}

function safeScope(value: string) {
    return value.replace(/[^a-zA-Z0-9._-]/g, "_");
}
