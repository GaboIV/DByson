import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

/** A themed, select-only combobox. The popup escapes scroll/overflow containers. */
export function Select({
  value,
  options,
  onChange,
  label,
  className = "",
  disabled = false,
  id: controlId,
}: {
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  label: string;
  className?: string;
  disabled?: boolean;
  id?: string;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null),
    popup = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false),
    [search, setSearch] = useState(""),
    [active, setActive] = useState(0);
  const [position, setPosition] = useState({ left: 0, top: 0, width: 240, maxHeight: 320 });
  const typed = useRef({ value: "", at: 0 });
  const shown = options.filter((o) => o.label.toLocaleLowerCase().includes(search.toLocaleLowerCase()));
  const selected = options.find((o) => o.value === value);
  const activeOption = shown[active];
  function close(restore = false) {
    setOpen(false);
    setSearch("");
    if (restore) trigger.current?.focus();
  }
  function expand(last = false) {
    if (disabled) return;
    setSearch("");
    const index = options.findIndex((o) => o.value === value && !o.disabled);
    const enabled = options.map((o, i) => (o.disabled ? -1 : i)).filter((i) => i >= 0);
    setActive(last ? (enabled.at(-1) ?? 0) : index < 0 ? (enabled[0] ?? 0) : index);
    setOpen(true);
  }
  function choose(option: SelectOption) {
    if (!option.disabled) {
      close(true);
      onChange(option.value);
    }
  }
  function keyboard(event: KeyboardEvent<HTMLElement>) {
    const input = event.target instanceof HTMLInputElement;
    if (event.key === "Escape" && open) {
      event.preventDefault();
      event.stopPropagation();
      close(true);
      return;
    }
    if (event.key === "Tab") {
      close();
      return;
    }
    if (!open && ["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
      event.preventDefault();
      expand(event.key === "ArrowUp");
      return;
    }
    if (open && ["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key) && (!input || event.key.startsWith("Arrow"))) {
      event.preventDefault();
      const indices = shown.map((o, i) => (o.disabled ? -1 : i)).filter((i) => i >= 0);
      if (!indices.length) return;
      const index = indices.indexOf(active);
      setActive(
        event.key === "Home"
          ? indices[0]
          : event.key === "End"
            ? indices.at(-1)!
            : indices[(index + (event.key === "ArrowDown" ? 1 : -1) + indices.length) % indices.length],
      );
      return;
    }
    if (open && (event.key === "Enter" || (!input && event.key === " "))) {
      event.preventDefault();
      if (activeOption) choose(activeOption);
      return;
    }
    if (!input && event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault();
      const now = Date.now();
      typed.current = { value: now - typed.current.at < 700 ? typed.current.value + event.key : event.key, at: now };
      if (!open) expand();
      const index = options.findIndex(
        (o) => !o.disabled && o.label.toLocaleLowerCase().startsWith(typed.current.value.toLocaleLowerCase()),
      );
      if (index >= 0) setActive(index);
    }
  }
  useLayoutEffect(() => {
    if (!open) return;
    const box = trigger.current!.getBoundingClientRect();
    const width = Math.min(Math.max(box.width, 240), innerWidth - 16);
    const below = innerHeight - box.bottom - 12,
      above = box.top - 12;
    const up = below < 180 && above > below;
    const maxHeight = Math.max(0, Math.min(320, up ? above : below));
    const height = Math.min(popup.current!.getBoundingClientRect().height, maxHeight);
    setPosition({
      width,
      maxHeight,
      left: Math.max(8, Math.min(box.left, innerWidth - width - 8)),
      top: up ? box.top - height - 4 : box.bottom + 4,
    });
  }, [open, search, options.length]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: Event) => {
      if (!popup.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) close();
    };
    const scroll = (event: Event) => {
      if (!popup.current?.contains(event.target as Node)) close();
    };
    const resize = () => close();
    document.addEventListener("pointerdown", outside);
    document.addEventListener("focusin", outside);
    window.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", resize);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("focusin", outside);
      window.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", resize);
    };
  }, [open]);
  useEffect(() => {
    if (open) document.getElementById(`${id}-${active}`)?.scrollIntoView({ block: "nearest" });
  }, [active, open, search, id]);
  useEffect(() => {
    if (disabled) close();
  }, [disabled]);
  return (
    <>
      <button
        id={controlId}
        ref={trigger}
        type="button"
        role="combobox"
        aria-label={label}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-controls={open ? id : undefined}
        aria-activedescendant={open && activeOption ? `${id}-${active}` : undefined}
        disabled={disabled}
        className={`themed-select ${className}`}
        title={selected?.label || label}
        data-value={value}
        onClick={() => (open ? close() : expand())}
        onKeyDown={keyboard}
      >
        <span className="themed-select-value">{selected?.label || "Seleccionar…"}</span>
        <span className="select-chevron" aria-hidden="true">
          ⌄
        </span>
      </button>
      {open &&
        createPortal(
          <div ref={popup} className="select-popover" style={position} onKeyDown={keyboard}>
            {options.length > 9 && (
              <div className="select-search">
                <input
                  type="search"
                  aria-label={`Buscar en ${label}`}
                  placeholder="Buscar…"
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                    setActive(0);
                  }}
                />
              </div>
            )}
            <div id={id} className="select-options" role="listbox" aria-label={label}>
              {shown.map((option, index) => (
                <div
                  id={`${id}-${index}`}
                  key={option.value}
                  role="option"
                  aria-selected={option.value === value}
                  aria-disabled={option.disabled || undefined}
                  className={`select-option ${active === index ? "is-active" : ""}`}
                  onPointerMove={() => {
                    if (!option.disabled) setActive(index);
                  }}
                  onClick={() => choose(option)}
                >
                  <span className="select-check" aria-hidden="true">
                    {option.value === value ? "✓" : ""}
                  </span>
                  <span>{option.label}</span>
                </div>
              ))}
            </div>
            {!shown.length && (
              <p className="select-empty" role="status">
                No hay coincidencias.
              </p>
            )}
          </div>,
          document.body,
        )}
    </>
  );
}
