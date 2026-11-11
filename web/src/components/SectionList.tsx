import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
  type Announcements,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { SectionIssues } from "../schema";
import type { SectionDefinition, SectionInstance } from "../types";
import s from "./SectionList.module.css";
import ui from "../ui.module.css";

interface Props {
  /** Fixed sections above and below the page body (shared look of the whole store). */
  header?: SectionInstance[];
  footer?: SectionInstance[];
  sections: SectionInstance[];
  defs: Map<string, SectionDefinition>;
  issues: Map<number, SectionIssues>;
  selected: string | null;
  onSelect: (id: string) => void;
  onMove: (from: number, to: number) => void;
  onDuplicate: (index: number) => void;
  onRemove: (index: number) => void;
  onAdd: () => void;
}

export function nameOf(sec: SectionInstance, defs: Map<string, SectionDefinition>): string {
  if (sec.globalRef) return `Global: ${sec.globalRef}`;
  return defs.get(sec.type ?? "")?.name ?? sec.type ?? sec.id;
}

export function SectionList(p: Props) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const ids = p.sections.map((x) => x.id);
  const label = (id: string | number) => {
    const sec = p.sections.find((x) => x.id === id);
    return sec ? nameOf(sec, p.defs) : String(id);
  };
  const pos = (id: string | number) => ids.indexOf(String(id)) + 1;
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Tomaste ${label(active.id)}, posición ${pos(active.id)} de ${ids.length}.`,
    onDragOver: ({ active, over }) => (over ? `${label(active.id)} sobre la posición ${pos(over.id)}.` : `${label(active.id)} fuera de la lista.`),
    onDragEnd: ({ active, over }) => (over ? `${label(active.id)} quedó en la posición ${pos(over.id)}.` : "Movimiento cancelado."),
    onDragCancel: ({ active }) => `Movimiento de ${label(active.id)} cancelado.`,
  };

  function onDragEnd(e: DragEndEvent) {
    if (!e.over || e.active.id === e.over.id) return;
    p.onMove(ids.indexOf(String(e.active.id)), ids.indexOf(String(e.over.id)));
  }

  return (
    <nav className={s.list} aria-label="Secciones de la página">
      <div className={s.head}>
        <h2>Secciones</h2>
        <button className={`${ui.btn} ${ui.primary}`} onClick={p.onAdd}>
          + Agregar
        </button>
      </div>
      <p className={s.hint}>Arrastra para reordenar. Con teclado: enfoca el asa, Espacio, flechas y Espacio.</p>
      {(p.header ?? []).length > 0 && (
        <ul className={s.items} aria-label="Encabezado" data-testid="fixed-header">
          {(p.header ?? []).map((sec) => (
            <li key={sec.id} className={`${s.row} ${p.selected === sec.id ? s.selected : ""}`}>
              <button className={s.name} onClick={() => p.onSelect(sec.id)} aria-current={p.selected === sec.id ? "true" : undefined}>
                {nameOf(sec, p.defs)} <small>· encabezado</small>
              </button>
            </li>
          ))}
        </ul>
      )}

      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={onDragEnd}
        accessibility={{
          announcements,
          screenReaderInstructions: {
            draggable: "Pulsa Espacio para tomar la sección, flechas para moverla y Espacio para soltarla. Escape cancela.",
          },
        }}
      >
        <SortableContext items={ids} strategy={verticalListSortingStrategy}>
          <ol className={s.items} data-testid="section-list">
            {p.sections.map((sec, i) => (
              <Row
                key={sec.id}
                sec={sec}
                index={i}
                name={nameOf(sec, p.defs)}
                issues={p.issues.get(i)}
                selected={p.selected === sec.id}
                onSelect={() => p.onSelect(sec.id)}
                onDuplicate={() => p.onDuplicate(i)}
                onRemove={() => p.onRemove(i)}
              />
            ))}
          </ol>
        </SortableContext>
      </DndContext>
      {p.sections.length === 0 && <p className={s.empty}>La página no tiene secciones. Agrega la primera.</p>}
      {(p.footer ?? []).length > 0 && (
        <ul className={s.items} aria-label="Pie de página" data-testid="fixed-footer">
          {(p.footer ?? []).map((sec) => (
            <li key={sec.id} className={`${s.row} ${p.selected === sec.id ? s.selected : ""}`}>
              <button className={s.name} onClick={() => p.onSelect(sec.id)} aria-current={p.selected === sec.id ? "true" : undefined}>
                {nameOf(sec, p.defs)} <small>· pie de página</small>
              </button>
            </li>
          ))}
        </ul>
      )}

    </nav>
  );
}

interface RowProps {
  sec: SectionInstance;
  index: number;
  name: string;
  issues?: SectionIssues;
  selected: boolean;
  onSelect: () => void;
  onDuplicate: () => void;
  onRemove: () => void;
}

function Row({ sec, name, issues, selected, onSelect, onDuplicate, onRemove }: RowProps) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: sec.id,
    disabled: sec.locked,
  });
  const count = (issues?.section.length ?? 0) + Object.values(issues?.fields ?? {}).reduce((n, v) => n + v.length, 0);
  const blocking =
    issues?.section.some((i) => i.blocking) || Object.values(issues?.fields ?? {}).some((v) => v.some((i) => i.blocking));
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`${s.row} ${selected ? s.selected : ""} ${isDragging ? s.dragging : ""}`}
      data-section-id={sec.id}
    >
      <button
        ref={setActivatorNodeRef}
        className={s.handle}
        aria-label={`Mover ${name}`}
        disabled={sec.locked}
        {...attributes}
        {...listeners}
      >
        ⠿
      </button>
      <button className={s.name} onClick={onSelect} aria-current={selected ? "true" : undefined}>
        <span>{name}</span>
        {count > 0 && (
          <span className={`${ui.pill} ${blocking ? ui.bad : ui.warn}`} aria-label={`${count} problemas`}>
            {count}
          </span>
        )}
      </button>
      <span className={s.actions}>
        <button className={ui.iconBtn} onClick={onDuplicate} aria-label={`Duplicar ${name}`} title="Duplicar" disabled={!!sec.globalRef}>
          ⧉
        </button>
        <button className={`${ui.iconBtn} ${ui.danger}`} onClick={onRemove} aria-label={`Quitar ${name}`} title="Quitar" disabled={sec.locked}>
          ✕
        </button>
      </span>
    </li>
  );
}
