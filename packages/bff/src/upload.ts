export function uploadHeaders(file: File): Record<string, string> {
  let filename = "";
  for (const char of file.name.replace(/[\\/\u0000-\u001f\u007f]/g, "")) {
    if (new TextEncoder().encode(filename + char).length > 255) break;
    filename += char;
  }
  const contentType =
    /^[a-zA-Z0-9!#$&^_.+-]+\/[a-zA-Z0-9!#$&^_.+-]+$/.test(file.type) &&
    file.type.length <= 127
      ? file.type
      : "application/octet-stream";
  return {
    "Content-Type": "application/octet-stream",
    "X-SOJ-Filename": encodeURIComponent(filename || "upload").replace(
      /[!'()*]/g,
      (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`,
    ),
    "X-SOJ-Content-Type": contentType,
  };
}
