// Animated loading indicator. `block` centers it with room to breathe (empty views);
// the inline form sits next to a title or inside a button.
export function Spinner({ label, block = false }: { label?: string; block?: boolean }) {
  return (
    <span className={block ? "spinner-block" : "spinner-inline"} role="status">
      <span className="spinner" aria-hidden="true" />
      {label && <span>{label}</span>}
    </span>
  );
}
