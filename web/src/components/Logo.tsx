/** Shulada logo: the "Arco" mark (a storefront arch with the orange live-signal
 *  dot on its keystone) plus the wordmark. */
export function LogoMark({ size = 28, className = "" }: { size?: number; className?: string }) {
  const gid = `sh-grad-${size}`;
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" className={className} aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="var(--brand-indigo)" />
          <stop offset="1" stopColor="var(--brand-violet)" />
        </linearGradient>
      </defs>
      <rect width="48" height="48" rx="12" fill={`url(#${gid})`} />
      <path d="M14 37V24.5A10 10 0 0 1 34 24.5V37" fill="none" stroke="#fff" strokeWidth="6.2" strokeLinecap="round" />
      <circle cx="24" cy="12.2" r="3.4" fill="var(--brand-senal)" />
    </svg>
  );
}

export function Logo({ size = 28, className = "" }: { size?: number; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      <LogoMark size={size} />
      <span className="font-display font-bold tracking-tight" style={{ fontSize: size * 0.82, lineHeight: 1 }}>
        shulada
      </span>
      <span className="sr-only">Shulada</span>
    </span>
  );
}
