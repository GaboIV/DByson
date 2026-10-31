import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { EditorView, basicSetup } from "codemirror";
import { sql, MySQL } from "@codemirror/lang-sql";
import { autocompletion } from "@codemirror/autocomplete";
import { EditorState, Prec } from "@codemirror/state";
import { keymap } from "@codemirror/view";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";
import { createSqlCompletionSource, type SqlTableInfo } from "./sqlMetadata";

export interface SqlEditorProps {
  value: string;
  onChange: (val: string) => void;
  onSelectionChange: (selectedText: string) => void;
  onRun: () => void;
  tables: SqlTableInfo[];
  getColumns: (tableName: string) => string[];
  defaultTable?: string;
}

const dbysonHighlightStyle = HighlightStyle.define([
  { tag: t.keyword, color: "var(--accent)", fontWeight: "600" },
  { tag: t.string, color: "var(--ok)" },
  { tag: t.number, color: "var(--env-staging)" },
  { tag: t.comment, color: "var(--muted)", fontStyle: "italic" },
  { tag: t.operator, color: "var(--muted)" },
  { tag: t.punctuation, color: "var(--muted)" },
  { tag: t.definition(t.variableName), color: "var(--text)" },
  { tag: t.variableName, color: "var(--text)" },
  { tag: t.typeName, color: "var(--env-staging)" },
  { tag: t.standard(t.name), color: "var(--accent)" },
]);

const dbysonEditorTheme = EditorView.theme({
  "&": {
    height: "100%",
    fontSize: "0.875rem",
    fontFamily: 'ui-monospace, "Cascadia Code", Menlo, monospace',
    backgroundColor: "var(--surface)",
    color: "var(--text)",
    border: "1px solid var(--border)",
    borderRadius: "6px",
  },
  "&.cm-focused": {
    outline: "2px solid var(--accent)",
    outlineOffset: "-1px",
    borderColor: "var(--accent)",
  },
  ".cm-scroller": {
    fontFamily: "inherit",
    lineHeight: "1.45",
    overflow: "auto",
  },
  ".cm-content": {
    padding: "0.5rem 0",
    caretColor: "var(--accent)",
  },
  ".cm-line": {
    padding: "0 0.75rem",
  },
  ".cm-gutters": {
    backgroundColor: "var(--bg)",
    color: "var(--muted)",
    borderRight: "1px solid var(--border)",
    borderTopLeftRadius: "5px",
    borderBottomLeftRadius: "5px",
  },
  ".cm-activeLineGutter": {
    backgroundColor: "var(--surface-hover)",
    color: "var(--text)",
  },
  ".cm-activeLine": {
    backgroundColor: "color-mix(in srgb, var(--accent) 6%, transparent)",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": {
    backgroundColor: "color-mix(in srgb, var(--accent) 25%, transparent) !important",
  },
  ".cm-selectionMatch": {
    backgroundColor: "color-mix(in srgb, var(--accent) 15%, transparent)",
  },
  "&.cm-focused .cm-cursor": {
    borderLeftColor: "var(--accent)",
    borderLeftWidth: "2px",
  },
  ".cm-tooltip": {
    backgroundColor: "var(--surface)",
    border: "1px solid var(--border)",
    borderRadius: "6px",
    boxShadow: "0 6px 18px rgba(0,0,0,0.35)",
  },
  ".cm-tooltip-autocomplete": {
    "& > ul": {
      maxHeight: "14rem",
      padding: "4px",
      fontFamily: 'ui-monospace, "Cascadia Code", Menlo, monospace',
      fontSize: "0.825rem",
    },
    "& > ul > li": {
      padding: "4px 8px",
      borderRadius: "4px",
      display: "flex",
      alignItems: "center",
      gap: "8px",
    },
    "& > ul > li[aria-selected]": {
      backgroundColor: "var(--accent)",
      color: "#ffffff",
    },
  },
  ".cm-completionLabel": {
    fontWeight: "600",
  },
  ".cm-completionDetail": {
    marginLeft: "auto",
    fontSize: "0.75rem",
    opacity: 0.7,
    fontStyle: "normal",
  },
});

const HEIGHT_KEY = "dbyson.sql-editor-height";
const DEFAULT_HEIGHT = 155;
const MIN_HEIGHT = 80;
const maxHeight = () => Math.max(MIN_HEIGHT, Math.round(window.innerHeight * 0.75));
const clampHeight = (h: number) => Math.min(maxHeight(), Math.max(MIN_HEIGHT, Math.round(h)));

function storedHeight() {
  try {
    const n = Number(localStorage.getItem(HEIGHT_KEY));
    return Number.isFinite(n) && n > 0 ? clampHeight(n) : DEFAULT_HEIGHT;
  } catch {
    return DEFAULT_HEIGHT;
  }
}

/** Drag (or arrow keys) to make the editor taller or shorter; double-click restores the default. */
function useEditorHeight() {
  const [height, setHeight] = useState(storedHeight);
  const save = useCallback((h: number) => {
    const next = clampHeight(h);
    setHeight(next);
    try {
      localStorage.setItem(HEIGHT_KEY, String(next));
    } catch {
      /* remembered for this tab only */
    }
  }, []);
  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      const handle = e.currentTarget;
      const startY = e.clientY;
      const start = height;
      handle.setPointerCapture(e.pointerId);
      const move = (ev: PointerEvent) => setHeight(clampHeight(start + ev.clientY - startY));
      const up = (ev: PointerEvent) => {
        handle.removeEventListener("pointermove", move);
        handle.removeEventListener("pointerup", up);
        handle.removeEventListener("pointercancel", up);
        save(start + ev.clientY - startY);
      };
      handle.addEventListener("pointermove", move);
      handle.addEventListener("pointerup", up);
      handle.addEventListener("pointercancel", up);
    },
    [height, save],
  );
  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLDivElement>) => {
      const step = e.shiftKey ? 80 : 20;
      if (e.key === "ArrowDown") save(height + step);
      else if (e.key === "ArrowUp") save(height - step);
      else if (e.key === "Home") save(MIN_HEIGHT);
      else if (e.key === "End") save(maxHeight());
      else return;
      e.preventDefault();
    },
    [height, save],
  );
  return { height, onPointerDown, onKeyDown, reset: () => save(DEFAULT_HEIGHT) };
}

export function SqlEditor({ value, onChange, onSelectionChange, onRun, tables, getColumns, defaultTable }: SqlEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const resize = useEditorHeight();
  const viewRef = useRef<EditorView | null>(null);

  const tablesRef = useRef(tables);
  tablesRef.current = tables;

  const getColumnsRef = useRef(getColumns);
  getColumnsRef.current = getColumns;

  const defaultTableRef = useRef(defaultTable);
  defaultTableRef.current = defaultTable;

  const onRunRef = useRef(onRun);
  onRunRef.current = onRun;

  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const onSelectionChangeRef = useRef(onSelectionChange);
  onSelectionChangeRef.current = onSelectionChange;

  useEffect(() => {
    if (!containerRef.current) return;

    const runKeymap = keymap.of([
      {
        key: "Mod-Enter",
        run: () => {
          onRunRef.current();
          return true;
        },
      },
    ]);

    const customCompletion = createSqlCompletionSource({
      getTables: () => tablesRef.current,
      getColumns: (t) => getColumnsRef.current(t),
      defaultTable: defaultTableRef.current,
    });

    const state = EditorState.create({
      doc: value,
      extensions: [
        basicSetup,
        // Override basicSetup's Mod-Enter (insertBlankLine).
        Prec.highest(runKeymap),
        dbysonEditorTheme,
        syntaxHighlighting(dbysonHighlightStyle),
        sql({ dialect: MySQL }),
        autocompletion({
          override: [customCompletion],
          defaultKeymap: true,
          icons: true,
        }),
        EditorView.updateListener.of((update) => {
          if (update.selectionSet || update.docChanged) {
            const mainSel = update.state.selection.main;
            const text = update.state.sliceDoc(mainSel.from, mainSel.to);
            onSelectionChangeRef.current(text);
          }
          if (update.docChanged) {
            onChangeRef.current(update.state.doc.toString());
          }
        }),
      ],
    });

    const view = new EditorView({
      state,
      parent: containerRef.current,
    });

    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, []);

  // Update editor text when value changes from outside (e.g. snippet, history, file import)
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const currentDoc = view.state.doc.toString();
    if (value !== currentDoc) {
      view.dispatch({
        changes: { from: 0, to: currentDoc.length, insert: value },
      });
    }
  }, [value]);

  return (
    <div className="sql-cm-resizable">
      <div ref={containerRef} className="sql-cm-wrapper" style={{ height: resize.height }} />
      <div
        className="sql-cm-resize"
        role="separator"
        aria-orientation="horizontal"
        aria-label="Cambiar la altura del editor SQL"
        aria-valuemin={MIN_HEIGHT}
        aria-valuenow={resize.height}
        tabIndex={0}
        title="Arrastra para cambiar la altura · doble clic para restablecer"
        onPointerDown={resize.onPointerDown}
        onKeyDown={resize.onKeyDown}
        onDoubleClick={resize.reset}
      />
    </div>
  );
}
