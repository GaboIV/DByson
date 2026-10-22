import { useEffect, useRef, type ReactNode } from "react";
export interface TabItem {
  id: string;
  label: string;
  title?: string;
  control?: string;
  leading?: ReactNode;
  dirty?: boolean;
  onSelect: () => void;
  onClose?: () => void;
  onRename?: () => void;
}
export function TabStrip({
  label,
  items,
  active,
  idPrefix = "tab",
  className = "",
  actions,
}: {
  label: string;
  items: TabItem[];
  active: string;
  idPrefix?: string;
  className?: string;
  actions?: ReactNode;
}) {
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const strip = list.current,
      selected = strip?.querySelector('[aria-selected="true"]');
    if (!strip?.getClientRects().length || !selected) return;
    const bounds = strip.getBoundingClientRect(),
      tab = selected.getBoundingClientRect();
    if (tab.left < bounds.left) strip.scrollLeft += tab.left - bounds.left;
    else if (tab.right > bounds.right) strip.scrollLeft += tab.right - bounds.right;
  }, [active, items]);
  return (
    <div className={`tab-strip ${className}`}>
      <div
        ref={list}
        className="tab-strip-scroll"
        role="tablist"
        aria-label={label}
        onKeyDown={(e) => {
          if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key) || (e.target as HTMLElement).getAttribute("role") !== "tab")
            return;
          const buttons = [...e.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
          const i = buttons.indexOf(e.target as HTMLButtonElement);
          const next =
            e.key === "Home"
              ? 0
              : e.key === "End"
                ? buttons.length - 1
                : (i + (e.key === "ArrowRight" ? 1 : -1) + buttons.length) % buttons.length;
          e.preventDefault();
          buttons[next]?.focus();
          buttons[next]?.click();
        }}
      >
        {items.map((item) => (
          <div
            key={item.id}
            className={`tab-item ${active === item.id ? "selected" : ""}`}
            onMouseDown={(e) => {
              if (e.button === 1 && item.onClose) e.preventDefault();
            }}
            onAuxClick={(e) => {
              if (e.button === 1 && item.onClose) {
                e.preventDefault();
                e.stopPropagation();
                item.onClose();
              }
            }}
          >
            <button
              id={`${idPrefix}-${item.id}`}
              role="tab"
              aria-label={item.label}
              aria-description={item.dirty ? "Cambios sin guardar" : undefined}
              aria-selected={active === item.id}
              aria-controls={item.control}
              tabIndex={active === item.id ? 0 : -1}
              title={item.onRename ? `${item.title || item.label} · Doble clic para renombrar` : item.title || item.label}
              onClick={item.onSelect}
              onDoubleClick={item.onRename}
              onKeyDown={(e) => {
                if (e.key === "F2" && item.onRename) {
                  e.preventDefault();
                  item.onRename();
                }
              }}
            >
              {item.leading}
              <span className="tab-item-label">{item.label}</span>
              {item.dirty && (
                <span className="tab-dirty" aria-label="Cambios sin guardar">
                  •
                </span>
              )}
            </button>
            {item.onClose && (
              <button
                className="tab-close"
                aria-label={`Cerrar pestaña ${item.label}`}
                title="Cerrar · También con clic de la rueda"
                onClick={item.onClose}
              >
                ×
              </button>
            )}
          </div>
        ))}
      </div>
      {actions && <div className="tab-strip-actions">{actions}</div>}
    </div>
  );
}
