import { afterEach, describe, expect, it, vi } from "vitest";
import { request, UNAUTHORIZED_EVENT } from "./api";

// Without a session the panel asks /v1/me once and shows the login screen. Its
// 401 used to announce "signed out", the listener asked /v1/me again, and the
// login screen sent about 430 requests a second.
describe("401 handling", () => {
  const seen: string[] = [];
  const win = Object.assign(new EventTarget(), { location: { hash: "" } });
  win.addEventListener(UNAUTHORIZED_EVENT, () => seen.push("unauthorized"));

  afterEach(() => {
    seen.length = 0;
    vi.unstubAllGlobals();
  });

  const unauthorized = () => new Response(JSON.stringify({ status: 401, code: "unauthenticated", title: "Unauthorized" }), { status: 401, headers: { "Content-Type": "application/problem+json" } });

  it("does not announce it when /v1/me itself answers 401", async () => {
    vi.stubGlobal("window", win);
    vi.stubGlobal("fetch", vi.fn(async () => unauthorized()));
    await expect(request("GET", "/v1/me")).rejects.toMatchObject({ status: 401 });
    expect(seen).toEqual([]);
  });

  it("announces it when any other call finds the session gone", async () => {
    vi.stubGlobal("window", win);
    vi.stubGlobal("fetch", vi.fn(async () => unauthorized()));
    await expect(request("GET", "/v1/orders")).rejects.toMatchObject({ status: 401 });
    expect(seen).toEqual(["unauthorized"]);
  });
});
