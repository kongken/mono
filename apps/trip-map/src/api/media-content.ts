// CDN reads deliberately omit site credentials, even for a same-origin CDN.
export async function mediaBlob(media: {
  url: string;
  data: Uint8Array;
  contentType: string;
}): Promise<Blob> {
  if (media.url) {
    const response = await fetch(media.url, {
      credentials: "omit",
      referrerPolicy: "no-referrer",
    });
    if (!response.ok) throw new Error("媒体下载失败，请稍后重试");
    return response.blob();
  }
  return new Blob([new Uint8Array(media.data)], { type: media.contentType });
}
