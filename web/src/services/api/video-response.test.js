import { describe, expect, it } from "bun:test";

import { readVideoErrorMessage, readVideoStatus, readVideoTaskId, readVideoUrl } from "./video-response";

describe("video response parser", () => {
    it("reads nested task data and stringified content urls", () => {
        const payload = {
            data: {
                task_id: "task_123",
                status: "Succeeded",
                content: '{"video_url":"https://cdn.example.com/generated.mp4"}',
            },
        };

        expect(readVideoTaskId(payload)).toBe("task_123");
        expect(readVideoStatus(payload)).toBe("succeeded");
        expect(readVideoUrl(payload)).toBe("https://cdn.example.com/generated.mp4");
        expect(readVideoErrorMessage(payload)).toBe("");
    });

    it("ignores progress messages while a task is still running", () => {
        const payload = {
            status: "processing",
            message: "Processing",
        };

        expect(readVideoErrorMessage(payload)).toBe("");
    });

    it("extracts gateway errors from stringified json and fallback text fields", () => {
        expect(
            readVideoErrorMessage({
                message: '{"error":{"message":"quota exceeded"}}',
            }),
        ).toBe("quota exceeded");

        expect(
            readVideoErrorMessage({
                result_url: "generation failed: quota exceeded",
            }),
        ).toBe("generation failed: quota exceeded");
    });
});
