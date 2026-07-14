import { NextRequest } from "next/server";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const IMAGE_FETCH_TIMEOUT_MS = 15_000;
const MAX_IMAGE_BYTES = 12 * 1024 * 1024;
const ALLOWED_HOSTS = new Set(["raw.githubusercontent.com", "pbs.twimg.com", "cdn.imgedify.com", "cms-assets.youmind.com", "marketing-assets.youmind.com"]);

export async function GET(request: NextRequest) {
    const value = request.nextUrl.searchParams.get("url") || "";
    let target: URL;
    try {
        target = new URL(value);
    } catch {
        return new Response("图片地址无效", { status: 400 });
    }
    if (target.protocol !== "https:" || !ALLOWED_HOSTS.has(target.hostname.toLowerCase())) {
        return new Response("图片地址不受支持", { status: 400 });
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), IMAGE_FETCH_TIMEOUT_MS);
    try {
        const response = await fetch(target, {
            headers: {
                Accept: "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
                "User-Agent": "Mozilla/5.0 (compatible; InfiniteCanvas/1.0)",
            },
            cache: "force-cache",
            signal: controller.signal,
        });
        if (!response.ok) return new Response("图片加载失败", { status: response.status });
        const contentType = response.headers.get("content-type")?.split(";", 1)[0].trim().toLowerCase() || "";
        if (!contentType.startsWith("image/")) return new Response("响应不是图片", { status: 415 });
        const contentLength = Number(response.headers.get("content-length"));
        if (Number.isFinite(contentLength) && contentLength > MAX_IMAGE_BYTES) return new Response("图片过大", { status: 413 });
        const data = await response.arrayBuffer();
        if (data.byteLength > MAX_IMAGE_BYTES) return new Response("图片过大", { status: 413 });
        return new Response(data, {
            headers: {
                "Cache-Control": "public, max-age=86400, stale-while-revalidate=604800",
                "Content-Type": contentType,
                "X-Content-Type-Options": "nosniff",
            },
        });
    } catch (error) {
        if (error instanceof Error && error.name === "AbortError") return new Response("图片加载超时", { status: 504 });
        return new Response("图片加载失败", { status: 502 });
    } finally {
        clearTimeout(timer);
    }
}
