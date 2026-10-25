import { Code, ConnectError } from "@connectrpc/connect";
import { classifyAccountError } from "@kongken/account/errors";
import { ReferencedDetailSchema, RevisionConflictDetailSchema, ValidationDetailSchema } from "@kongken/api-client/bff/metro/v1/admin_pb";

export type FieldProblem = { field: string; reason: string };

export function fieldProblems(e: unknown): FieldProblem[] {
  return ConnectError.from(e)
    .findDetails(ValidationDetailSchema)
    .flatMap((d) => d.errors.map((x) => ({ field: x.field, reason: x.reason })));
}

/** The current revision when the call lost a race with another editor. */
export function conflictRevision(e: unknown): bigint | undefined {
  return ConnectError.from(e).findDetails(RevisionConflictDetailSchema)[0]?.currentRevision;
}

/** The refusals about who the caller is and where the call comes from: they have one screen (Problem) in the console. */
export function isSessionLevel(e: unknown): boolean {
  return ["reauth_required", "not_signed_in", "forbidden", "not_allowed"].includes(classifyAccountError(e).kind);
}

/** True when the record the call named does not exist (any more). */
export const isNotFound = (e: unknown) => ConnectError.from(e).code === Code.NotFound;

/** True when metro is not mounted behind this BFF: the call was answered "no such service". */
export const isAbsent = (e: unknown) => ConnectError.from(e).code === Code.Unimplemented;

const entityNames: Record<string, string> = { city: "城市", rolling_stock: "车型", line: "线路", topic: "专题", rider_line: "乘坐记录" };

/** What a catalogue call's failure says, with the catalogue's own refusals (conflict, references, field errors) spelled out. */
export function describeMetroError(e: unknown): string {
  const err = ConnectError.from(e);
  if (conflictRevision(e) !== undefined) return "这条记录已被他人修改。";
  const refs = err.findDetails(ReferencedDetailSchema)[0];
  if (refs && refs.references.length > 0) {
    return `仍被引用，不能删除：${refs.references.map((r) => `${entityNames[r.entityType] ?? r.entityType} ${r.count}`).join("、")}。`;
  }
  const problems = fieldProblems(e);
  if (problems.length > 0) return problems.map((p) => `${p.field}：${p.reason}`).join("；");
  switch (err.code) {
    case Code.AlreadyExists:
      return err.rawMessage || "已存在。";
    case Code.NotFound:
      return "没有找到，可能已被删除。";
    default:
      return classifyAccountError(e).message;
  }
}
