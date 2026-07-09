import type { NextRequest } from "next/server";

export const runtime = "nodejs";
export const maxDuration = 1200;

type VideoContentRequest = {
    url?: string;
    apiKey?: string;
};

export async function POST(request: NextRequest) {
    let payload: VideoContentRequest;
    try {
        payload = (await request.json()) as VideoContentRequest;
    } catch {
        return Response.json({ error: { message: "视频下载请求无效" } }, { status: 400 });
    }

    const url = String(payload.url || "").trim();
    const apiKey = String(payload.apiKey || "").trim();
    if (!/^https?:\/\//i.test(url)) {
        return Response.json({ error: { message: "视频下载地址无效" } }, { status: 400 });
    }
    if (!apiKey) {
        return Response.json({ error: { message: "视频下载缺少 API Key" } }, { status: 400 });
    }

    try {
        const upstream = await fetch(url, {
            headers: { Authorization: `Bearer ${apiKey}` },
            cache: "no-store",
        });
        const headers = new Headers();
        for (const key of ["content-type", "content-disposition", "content-length"]) {
            const value = upstream.headers.get(key);
            if (value) headers.set(key, value);
        }
        return new Response(upstream.body, {
            status: upstream.status,
            statusText: upstream.statusText,
            headers,
        });
    } catch {
        return Response.json({ error: { message: "视频下载失败，请稍后重试" } }, { status: 502 });
    }
}
