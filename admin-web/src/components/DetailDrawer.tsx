import { useEffect, type ReactNode } from "react";
import { Button } from "./ui";

export function DetailDrawer({
  open,
  title,
  onClose,
  children,
  widthClass = "max-w-xl",
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  widthClass?: string;
}) {
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-40 flex justify-end" data-testid="detail-drawer">
      <button
        type="button"
        className="absolute inset-0 bg-black/50"
        aria-label="Close detail drawer"
        onClick={onClose}
      />
      <aside
        className={`relative z-10 flex h-full w-full ${widthClass} flex-col border-l bg-card shadow-xl`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h2 className="truncate text-sm font-semibold">{title}</h2>
          <Button type="button" size="sm" variant="ghost" onClick={onClose} data-testid="drawer-close">
            Close
          </Button>
        </div>
        <div className="flex-1 overflow-auto p-4">{children}</div>
      </aside>
    </div>
  );
}
