import type { NextRequest } from "next/server";
import { request as httpRequest } from "node:http";
import { request as httpsRequest } from "node:https";
import type { IncomingMessage, OutgoingHttpHeaders } from "node:http";
import { Readable } from "node:stream";

export const runtime = "nodejs";
export const maxDuration = 1200;

const DEFAULT_PROXY_TIMEOUT_MS = 20 * 60 * 1000;

type RouteContext = {
    params: Promise<{ path: string[] }>;
};

function proxyHeaders(request: NextRequest) {
    const headers = new Headers(request.headers);
    headers.delete("host");
    headers.delete("content-length");
    headers.delete("connection");
    headers.set("x-forwarded-host", request.nextUrl.host);
    headers.set("x-forwarded-proto", request.nextUrl.protocol.replace(":", ""));
    return headers;
}

function proxyTimeoutMs() {
    const value = Number(process.env.API_PROXY_TIMEOUT_MS);
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_PROXY_TIMEOUT_MS;
}

function nodeHeaders(headers: Headers): OutgoingHttpHeaders {
    const result: OutgoingHttpHeaders = {};
    headers.forEach((value, key) => {
        result[key] = value;
    });
    return result;
}

function responseHeaders(response: IncomingMessage) {
    const headers = new Headers();
    for (const [key, value] of Object.entries(response.headers)) {
        if (!value) continue;
        if (Array.isArray(value)) value.forEach((item) => headers.append(key, item));
        else headers.set(key, value);
    }
    headers.delete("content-length");
    headers.delete("content-encoding");
    headers.delete("transfer-encoding");
    return headers;
}

async function proxy(request: NextRequest, context: RouteContext) {
    const { path } = await context.params;
    const apiBaseUrl = process.env.API_BASE_URL || "http://127.0.0.1:8080";
    const target = `${apiBaseUrl.replace(/\/$/, "")}/api/${path.map(encodeURIComponent).join("/")}${request.nextUrl.search}`;
    const hasBody = request.method !== "GET" && request.method !== "HEAD";

    try {
        return await proxyWithNodeHttp(target, request, hasBody);
    } catch (error) {
        console.error("Failed to proxy", target, error);
        const message = error instanceof Error && error.message.includes("timeout") ? "接口等待超时，请稍后重试，或检查上游模型是否长时间未返回" : "接口连接失败，请确认后端服务已启动";
        return Response.json({ code: 1, data: null, msg: message }, { status: 502 });
    }
}

function proxyWithNodeHttp(target: string, request: NextRequest, hasBody: boolean) {
    const url = new URL(target);
    const transport = url.protocol === "https:" ? httpsRequest : httpRequest;

    return new Promise<Response>((resolve, reject) => {
        const upstream = transport(
            url,
            {
                method: request.method,
                headers: nodeHeaders(proxyHeaders(request)),
            },
            (response) => {
                resolve(
                    new Response(Readable.toWeb(response) as ReadableStream<Uint8Array>, {
                        status: response.statusCode || 502,
                        statusText: response.statusMessage,
                        headers: responseHeaders(response),
                    }),
                );
            },
        );

        upstream.on("error", reject);
        upstream.setTimeout(proxyTimeoutMs(), () => {
            upstream.destroy(new Error(`API proxy timeout after ${proxyTimeoutMs()}ms`));
        });

        if (hasBody && request.body) {
            const body = Readable.fromWeb(request.body as Parameters<typeof Readable.fromWeb>[0]);
            body.on("error", (error) => upstream.destroy(error));
            body.pipe(upstream);
            return;
        }
        upstream.end();
    });
}

export const GET = proxy;
export const HEAD = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
export const OPTIONS = proxy;
