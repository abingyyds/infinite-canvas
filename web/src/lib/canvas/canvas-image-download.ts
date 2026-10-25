import { fitNodeSize } from "@/lib/canvas/canvas-node-size";
import { imageMetadata } from "@/lib/canvas/canvas-node-factory";
import type { ImageDownloadSource } from "@/services/api/image-download";
import type { UploadedImage } from "@/services/image-storage";
import type { CanvasNodeData } from "@/types/canvas";

export function pendingImageDownload(node: CanvasNodeData, imageId?: string) {
    if (imageId) return node.metadata?.images?.find((image) => image.id === imageId)?.pendingImageDownload;
    return node.metadata?.pendingImageDownload || node.metadata?.images?.find((image) => image.status === "error" && image.pendingImageDownload)?.pendingImageDownload;
}

export function rememberImageDownload(node: CanvasNodeData, source: ImageDownloadSource, imageId?: string): CanvasNodeData {
    return {
        ...node,
        metadata: {
            ...node.metadata,
            ...(imageId
                ? { images: node.metadata?.images?.map((image) => image.id === imageId ? { ...image, pendingImageDownload: source } : image) }
                : { pendingImageDownload: source }),
        },
    };
}

export function applyDownloadedImage(node: CanvasNodeData, uploaded: UploadedImage, imageId?: string): CanvasNodeData {
    const makePrimary = !node.metadata?.content || !imageId || node.metadata?.primaryImageId === imageId;
    const size = node.metadata?.freeResize ? { width: node.width, height: node.height } : fitNodeSize(uploaded.width, uploaded.height, Math.max(node.width, node.height), Math.max(node.width, node.height));
    const images = node.metadata?.images?.map((image) => image.id === imageId ? { ...image, ...imageMetadata(uploaded), status: "success" as const, errorDetails: undefined } : image);
    return {
        ...node,
        ...(makePrimary ? { ...size, position: { x: node.position.x + (node.width - size.width) / 2, y: node.position.y + (node.height - size.height) / 2 } } : {}),
        metadata: {
            ...node.metadata,
            ...(makePrimary ? { ...imageMetadata(uploaded), primaryImageId: imageId } : {}),
            images,
            status: images?.some((image) => image.status === "loading") ? "loading" : "success",
            errorDetails: undefined,
        },
    };
}
