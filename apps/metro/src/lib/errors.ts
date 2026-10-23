import { Code, ConnectError } from "@connectrpc/connect";
import {
  ReferencedDetailSchema,
  RevisionConflictDetailSchema,
  ValidationDetailSchema,
} from "@kongken/api-client/bff/metro/v1/admin_pb";

export type FieldProblem = { field: string; reason: string };

export const asConnectError = (e: unknown) => ConnectError.from(e);

export function isUnauthenticated(e: unknown) {
  return asConnectError(e).code === Code.Unauthenticated;
}

export function fieldProblems(e: unknown): FieldProblem[] {
  const err = asConnectError(e);
  return err
    .findDetails(ValidationDetailSchema)
    .flatMap((d) => d.errors.map((x) => ({ field: x.field, reason: x.reason })));
}

/** The current revision when the call lost a race with another editor. */
export function conflictRevision(e: unknown): bigint | undefined {
  return asConnectError(e).findDetails(RevisionConflictDetailSchema)[0]?.currentRevision;
}

const entityNames: Record<string, string> = { city: "城市", rolling_stock: "车型", line: "线路", topic: "专题", rider_line: "乘坐记录" };

export function describeError(e: unknown): string {
  const err = asConnectError(e);
  if (conflictRevision(e) !== undefined) return "已被他人修改，请刷新后重试。";
  const refs = err.findDetails(ReferencedDetailSchema)[0];
  if (refs && refs.references.length > 0) {
    return `仍被引用，不能删除：${refs.references.map((r) => `${entityNames[r.entityType] ?? r.entityType} ${r.count}`).join("、")}。`;
  }
  const problems = fieldProblems(e);
  if (problems.length > 0) return problems.map((p) => `${p.field}：${p.reason}`).join("；");
  switch (err.code) {
    case Code.Unauthenticated:
      return "请先登录，或重新登录后再试。";
    case Code.PermissionDenied:
      return "没有权限执行此操作。";
    case Code.NotFound:
      return "没有找到。";
    case Code.AlreadyExists:
      return err.rawMessage || "已存在。";
    case Code.ResourceExhausted:
      return "操作太频繁，请稍后再试。";
    case Code.Unavailable:
    case Code.DeadlineExceeded:
      return "服务暂时不可用，请稍后重试。";
    default:
      return err.rawMessage || "操作失败。";
  }
}
