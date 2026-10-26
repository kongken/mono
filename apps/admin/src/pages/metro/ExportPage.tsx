import { useState } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { describeMetroError, isSessionLevel } from "@/lib/metro-errors";
import { metroAdmin, write } from "@/lib/rpc";

/** Downloads the whole catalogue as metro-catalog/1 JSON, the same format the import page takes back. */
export default function ExportPage() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const download = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await write(() => metroAdmin.exportCatalog({}));
      const url = URL.createObjectURL(new Blob([res.catalogJson], { type: "application/json" }));
      const a = document.createElement("a");
      a.href = url;
      a.download = `metro-catalog-v${res.catalogVersion}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section aria-label="导出" className="grid gap-3">
      <p className="max-w-2xl text-sm text-muted-foreground">下载整个目录的 <code>metro-catalog/1</code> JSON，可作为备份，或导入到另一个环境。</p>
      {error !== undefined ? isSessionLevel(error) ? <Problem error={error} retry={() => void download()} /> : <Notice kind="error">{describeMetroError(error)}</Notice> : null}
      <div>
        <Button type="button" disabled={busy} onClick={() => void download()}>下载目录 JSON</Button>
      </div>
    </section>
  );
}
