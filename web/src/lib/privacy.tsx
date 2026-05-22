import { createContext, useContext, useState, type ReactNode } from 'react';

interface PrivacyCtx {
  masked: boolean;
  toggle: () => void;
}

const Ctx = createContext<PrivacyCtx>({ masked: false, toggle: () => {} });

export function PrivacyProvider({ children }: { children: ReactNode }) {
  const [masked, setMasked] = useState(false);
  return (
    <Ctx.Provider value={{ masked, toggle: () => setMasked((v) => !v) }}>{children}</Ctx.Provider>
  );
}

export function usePrivacy() {
  return useContext(Ctx);
}

/** Renders masked "₹••••••" or the real value based on privacy state. */
export function MaskedAmount({
  value,
  format,
  className,
}: {
  value: string | number | null | undefined;
  format?: (v: number) => string;
  className?: string;
}) {
  const { masked } = usePrivacy();
  if (masked) {
    return <span className={className}>₹ ••••••</span>;
  }
  const n = typeof value === 'string' ? Number(value) : (value ?? 0);
  const text = format ? format(n) : `₹${n.toLocaleString('en-IN', { maximumFractionDigits: 2 })}`;
  return <span className={className}>{text}</span>;
}
