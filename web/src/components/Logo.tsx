/** Apexx logo: the "cumbre" mark (a peak with a signal dot) plus the wordmark. */
export function LogoMark({ size = 28, className = "" }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" className={className} aria-hidden="true" focusable="false">
      <rect width="32" height="32" rx="8" fill="var(--brand-cumbre)" />
      <path d="M8.5 23 16 8.5 23.5 23" fill="none" stroke="#fff" strokeWidth="3.2" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M12.6 23 16 16.4 19.4 23" fill="none" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" opacity=".55" />
      <circle cx="16" cy="8.5" r="2.6" fill="var(--brand-senal)" />
    </svg>
  );
}

export function Logo({ size = 28, className = "" }: { size?: number; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      <LogoMark size={size} />
      <span className="font-display font-bold tracking-tight" style={{ fontSize: size * 0.82, lineHeight: 1 }}>
        apexx
      </span>
      <span className="sr-only">Apexx</span>
    </span>
  );
}
