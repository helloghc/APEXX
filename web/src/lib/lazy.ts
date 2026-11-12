import { createElement, lazy, type ComponentType } from "react";

/** A lazily loaded page that can be fetched ahead of time. */
export type LazyPage<P> = ComponentType<P> & { preload: () => Promise<void> };

/**
 * lazyPage is React.lazy plus preload: once the module is loaded the page
 * renders directly, without suspending. A nested Suspense boundary that
 * suspends inside content already on screen is revealed in React's 300 ms
 * batches, so a section whose code arrives in 5 ms would still wait 300 ms;
 * preloading avoids that wait.
 */
export function lazyPage<P extends object>(
  load: () => Promise<ComponentType<P>>,
): LazyPage<P> {
  let mod: ComponentType<P> | null = null;
  let pending: Promise<void> | null = null;
  const preload = () =>
    (pending ??= load().then(
      (m) => {
        mod = m;
      },
      (e) => {
        pending = null; // a failed fetch can be retried
        throw e;
      },
    ));
  const Fallback = lazy(() =>
    preload().then(() => ({ default: mod as ComponentType<P> })),
  );
  const Page = ((props: P) =>
    createElement((mod ?? Fallback) as ComponentType<P>, props)) as LazyPage<P>;
  Page.preload = preload;
  return Page;
}

/** whenIdle runs fn when the browser has nothing else to do. */
export function whenIdle(fn: () => void): () => void {
  if (typeof window.requestIdleCallback === "function") {
    const id = window.requestIdleCallback(fn, { timeout: 2000 });
    return () => window.cancelIdleCallback(id);
  }
  const t = window.setTimeout(fn, 200);
  return () => window.clearTimeout(t);
}
