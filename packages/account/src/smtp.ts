import net from "node:net";

export type CaughtMail = { to: string[]; subject: string; text: string; links: string[] };

/** Decodes the quoted-printable body mail servers produce (soft line breaks and =XX). */
export function decodeQuotedPrintable(input: string): string {
  const joined = input.replace(/=\r?\n/g, "");
  const bytes: number[] = [];
  for (let i = 0; i < joined.length; i++) {
    const c = joined[i]!;
    if (c === "=" && /^[0-9A-Fa-f]{2}$/.test(joined.slice(i + 1, i + 3))) {
      bytes.push(parseInt(joined.slice(i + 1, i + 3), 16));
      i += 2;
    } else {
      bytes.push(...Buffer.from(c));
    }
  }
  return Buffer.from(bytes).toString("utf8");
}

/** Pulls the readable text of a raw message: decodes the transfer encoding of each part. */
export function readMessage(raw: string): { subject: string; text: string } {
  const [head = "", ...rest] = raw.split(/\r?\n\r?\n/);
  const body = rest.join("\n\n");
  const header = (text: string, name: string) =>
    text.replace(/\r?\n[ \t]+/g, " ").match(new RegExp(`^${name}:\\s*(.*)$`, "im"))?.[1]?.trim() ?? "";
  const decode = (text: string, partHead: string) => {
    const enc = header(partHead, "Content-Transfer-Encoding").toLowerCase();
    if (enc === "quoted-printable") return decodeQuotedPrintable(text);
    if (enc === "base64") return Buffer.from(text.replace(/\s+/g, ""), "base64").toString("utf8");
    return text;
  };
  const boundary = header(head, "Content-Type").match(/boundary="?([^";]+)"?/i)?.[1];
  let text = "";
  if (boundary) {
    for (const part of body.split(`--${boundary}`)) {
      const [partHead = "", ...partBody] = part.replace(/^\r?\n/, "").split(/\r?\n\r?\n/);
      if (/text\/plain/i.test(partHead)) text += decode(partBody.join("\n\n"), partHead);
    }
  } else {
    text = decode(body, head);
  }
  return { subject: header(head, "Subject"), text };
}

/**
 * A loopback mail catcher: accepts mail over plain SMTP like the account service's
 * `smtp` provider sends it (tls_mode "plain", allow_insecure_loopback) and keeps it for the
 * test. Only for local runs; nothing leaves the machine.
 */
export async function startMailSink(port: number) {
  const mails: CaughtMail[] = [];
  const sockets = new Set<net.Socket>();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {});
    let to: string[] = [];
    let data: string | null = null;
    let buffer = "";
    const reply = (line: string) => socket.write(line + "\r\n");
    reply("220 mail-sink ready");
    socket.on("data", (chunk) => {
      buffer += chunk.toString("utf8");
      for (;;) {
        if (data !== null) {
          const end = buffer.indexOf("\r\n.\r\n");
          if (end < 0) {
            data += buffer;
            buffer = "";
            return;
          }
          data += buffer.slice(0, end);
          buffer = buffer.slice(end + 5);
          const raw = data.replace(/\r\n\.\./g, "\r\n.");
          data = null;
          const { subject, text } = readMessage(raw);
          mails.push({ to, subject, text, links: text.match(/https?:\/\/[^\s<>"']+/g) ?? [] });
          to = [];
          reply("250 queued");
          continue;
        }
        const nl = buffer.indexOf("\r\n");
        if (nl < 0) return;
        const line = buffer.slice(0, nl);
        buffer = buffer.slice(nl + 2);
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === "EHLO" || verb === "HELO") reply("250 mail-sink");
        else if (verb === "MAIL" || verb === "RSET") reply("250 ok");
        else if (verb === "RCPT") {
          to.push(line.match(/<([^>]*)>/)?.[1] ?? "");
          reply("250 ok");
        } else if (verb === "DATA") {
          data = "";
          reply("354 end with <CRLF>.<CRLF>");
        } else if (verb === "QUIT") {
          reply("221 bye");
          socket.end();
        } else reply("250 ok");
      }
    });
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", resolve);
  });
  return {
    mails,
    /** Waits for a mail to `address` whose link contains `path`, and returns that link. */
    async waitForLink(address: string, path: string, timeoutMs = 20_000): Promise<string> {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline) {
        const mail = [...mails].reverse().find((m) => m.to.includes(address.toLowerCase()));
        const link = mail?.links.find((l) => l.includes(path));
        if (link) return link;
        await new Promise((r) => setTimeout(r, 200));
      }
      throw new Error(`no mail with a ${path} link reached ${address} in time (${mails.length} mails caught)`);
    },
    async close() {
      for (const s of sockets) s.destroy();
      await new Promise<void>((resolve) => server.close(() => resolve()));
    },
  };
}
