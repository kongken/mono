import { useState } from "react";
import { Button } from "@kongken/ui/button";
import { admin, write } from "@/lib/rpc";
import { describeError } from "@/lib/errors";
import { Alert } from "@/components/extras";

export default function ExportPage() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const download = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await write(() => admin.exportCatalog({}));
      const url = URL.createObjectURL(new Blob([res.catalogJson], { type: "application/json" }));
      const a = document.createElement("a");
      a.href = url;
      a.download = `metro-catalog-v${res.catalogVersion}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section aria-label="导出">
      <p className="mb-3 max-w-2xl text-sm text-muted-foreground">下载整个目录的 <code>metro-catalog/1</code> JSON，可作为备份，或导入到另一个环境。</p>
      {error ? <Alert className="mb-3">{error}</Alert> : null}
      <Button disabled={busy} onClick={() => void download()}>下载目录 JSON</Button>
    </section>
  );
}
