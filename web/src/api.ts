import type { Op, Problem, Release, Report, SectionDefinition, Template } from "./types";
import { ProblemSchema } from "./types";

const KEY = "apexx.apiKey";
const ACTOR = "apexx.actor";

function store(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

export const credentials = {
  get key(): string {
    return store()?.getItem(KEY) ?? "";
  },
  get actor(): string {
    return store()?.getItem(ACTOR) ?? "";
  },
  save(key: string, actor: string) {
    try {
      store()?.setItem(KEY, key);
      store()?.setItem(ACTOR, actor);
    } catch {
      /* storage blocked: credentials last for this page only */
    }
  },
};

export class ApiError extends Error {
  constructor(public problem: Problem) {
    super(problem.detail || problem.title || problem.code);
  }
}

async function request<T>(method: string, path: string, body?: unknown, as: "json" | "text" = "json"): Promise<T> {
  const headers: Record<string, string> = { Accept: as === "json" ? "application/json" : "text/html" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (credentials.key) headers.Authorization = `Bearer ${credentials.key}`;
  if (credentials.actor) headers["X-Apexx-Actor"] = credentials.actor;
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!res.ok) {
    let parsed: Problem = { status: res.status, code: "http_" + res.status, title: res.statusText };
    try {
      const r = ProblemSchema.safeParse(await res.json());
      if (r.success) parsed = r.data;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(parsed);
  }
  return (as === "json" ? res.json() : res.text()) as Promise<T>;
}

const enc = encodeURIComponent;

export interface PatchResult {
  template: Template;
  report: Report;
  version: number;
}

export const api = {
  sections: () => request<{ items: SectionDefinition[] }>("GET", "/v1/builder/sections").then((r) => r.items),
  draft: (site: string, handle: string) =>
    request<{ template: Template; version: number }>("GET", `/v1/sites/${enc(site)}/templates/${enc(handle)}/draft`),
  patch: (site: string, handle: string, ops: Op[], ifVersion: number) =>
    request<PatchResult>("PATCH", `/v1/sites/${enc(site)}/templates/${enc(handle)}/draft`, { ops, ifVersion }),
  preview: (site: string, handle: string) =>
    request<string>("GET", `/v1/sites/${enc(site)}/templates/${enc(handle)}/preview`, undefined, "text"),
  publish: (site: string, message: string) =>
    request<{ release: Release; reports: Record<string, Report> }>(
      "POST",
      `/v1/sites/${enc(site)}/publish?message=${enc(message)}`,
    ),
  rollback: (site: string) => request<{ release: Release }>("POST", `/v1/sites/${enc(site)}/rollback`),
  releases: (site: string) =>
    request<{ items: Release[] | null }>("GET", `/v1/sites/${enc(site)}/releases`).then((r) => r.items ?? []),
};
