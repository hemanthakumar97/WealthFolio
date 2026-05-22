import { useState } from 'react';
import { createFileRoute } from '@tanstack/react-router';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Tags, Plus, Pencil, Trash2, ChevronDown, ChevronRight, X, Check } from 'lucide-react';

import {
  categoriesApi,
  instrumentsApi,
  type Category,
  type CategoryInstrument,
  type Instrument,
} from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

export const Route = createFileRoute('/_app/categories')({
  component: CategoriesPage,
});

const PRESET_COLORS = [
  '#6366f1',
  '#8b5cf6',
  '#ec4899',
  '#f43f5e',
  '#ef4444',
  '#f97316',
  '#f59e0b',
  '#84cc16',
  '#22c55e',
  '#14b8a6',
  '#06b6d4',
  '#3b82f6',
];

// --- Inline category form (create or edit) ---

interface CategoryFormProps {
  initial?: Category;
  onCancel: () => void;
  onSuccess: () => void;
}

function CategoryForm({ initial, onCancel, onSuccess }: CategoryFormProps) {
  const qc = useQueryClient();
  const [name, setName] = useState(initial?.name ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const [color, setColor] = useState(initial?.color ?? PRESET_COLORS[0]);

  const createMut = useMutation({
    mutationFn: () => categoriesApi.create({ name, description, color }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['categories'] });
      onSuccess();
    },
  });
  const updateMut = useMutation({
    mutationFn: () => categoriesApi.update(initial!.id, { name, description, color }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['categories'] });
      onSuccess();
    },
  });

  const isPending = createMut.isPending || updateMut.isPending;
  const error = createMut.error?.message ?? updateMut.error?.message;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    initial ? updateMut.mutate() : createMut.mutate();
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4 rounded-lg border border-border bg-card p-5">
      <h3 className="font-semibold">{initial ? 'Edit Category' : 'New Category'}</h3>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="cat-name">Name</Label>
          <Input
            id="cat-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Large Cap"
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="cat-desc">Description (optional)</Label>
          <Input
            id="cat-desc"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Short description"
          />
        </div>
      </div>
      <div className="space-y-2">
        <Label>Color</Label>
        <div className="flex flex-wrap items-center gap-2">
          {PRESET_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              onClick={() => setColor(c)}
              className="h-7 w-7 rounded-full transition-transform hover:scale-110"
              style={{
                backgroundColor: c,
                outline: color === c ? `3px solid ${c}` : '2px solid transparent',
                outlineOffset: 2,
              }}
            />
          ))}
          <Input
            value={color}
            onChange={(e) => setColor(e.target.value)}
            placeholder="#hex"
            className="w-32 font-mono text-sm"
          />
        </div>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button type="submit" disabled={isPending || !name.trim()}>
          {isPending ? 'Saving…' : initial ? 'Update' : 'Create'}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// --- Category card with expandable instruments ---

function CategoryCard({ category }: { category: Category }) {
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [editing, setEditing] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [addingInstr, setAddingInstr] = useState(false);
  const [selectedInstrId, setSelectedInstrId] = useState('');

  const { data: instruments = [] } = useQuery({
    queryKey: ['categories', category.id, 'instruments'],
    queryFn: () => categoriesApi.listInstruments(category.id),
    enabled: expanded,
  });

  const { data: allInstruments = [] } = useQuery({
    queryKey: ['instruments'],
    queryFn: () => instrumentsApi.list(),
    enabled: addingInstr,
  });

  const deleteMut = useMutation({
    mutationFn: () => categoriesApi.remove(category.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['categories'] });
    },
  });

  const removeInstrMut = useMutation({
    mutationFn: (instrId: number) => categoriesApi.removeInstrument(category.id, instrId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['categories', category.id, 'instruments'] });
      qc.invalidateQueries({ queryKey: ['categories'] });
    },
  });

  const addInstrMut = useMutation({
    mutationFn: () => categoriesApi.addInstrument(category.id, Number(selectedInstrId)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['categories', category.id, 'instruments'] });
      qc.invalidateQueries({ queryKey: ['categories'] });
      setSelectedInstrId('');
      setAddingInstr(false);
    },
  });

  const existingIds = new Set((instruments as CategoryInstrument[]).map((i) => i.instrument_id));
  const available = (allInstruments as Instrument[]).filter((i) => !existingIds.has(i.id));

  if (editing) {
    return (
      <CategoryForm
        initial={category}
        onCancel={() => setEditing(false)}
        onSuccess={() => setEditing(false)}
      />
    );
  }

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex items-center gap-4 px-5 py-4">
        <div
          className="h-4 w-4 shrink-0 rounded-full"
          style={{ backgroundColor: category.color ?? '#6366f1' }}
        />
        <div className="min-w-0 flex-1">
          <p className="font-semibold leading-tight">{category.name}</p>
          {category.description && (
            <p className="mt-0.5 truncate text-sm text-muted-foreground">{category.description}</p>
          )}
        </div>
        <Badge variant="secondary">{category.instrument_count} instruments</Badge>
        <div className="flex items-center gap-1">
          <Button size="icon" variant="ghost" onClick={() => setEditing(true)}>
            <Pencil className="size-4" />
          </Button>
          {confirmDelete ? (
            <div className="flex items-center gap-1 rounded-md bg-destructive/10 px-2 py-1">
              <span className="text-xs text-destructive">Delete?</span>
              <Button
                size="icon"
                variant="ghost"
                className="size-6 text-destructive"
                onClick={() => deleteMut.mutate()}
              >
                <Check className="size-3" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                className="size-6"
                onClick={() => setConfirmDelete(false)}
              >
                <X className="size-3" />
              </Button>
            </div>
          ) : (
            <Button size="icon" variant="ghost" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4 text-destructive" />
            </Button>
          )}
          <Button
            size="icon"
            variant="ghost"
            onClick={() => {
              setExpanded((v) => !v);
              setConfirmDelete(false);
            }}
          >
            {expanded ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
          </Button>
        </div>
      </div>

      {expanded && (
        <div className="space-y-3 border-t border-border px-5 pb-4 pt-3">
          <div className="flex items-center justify-between">
            <p className="text-sm font-medium text-muted-foreground">Instruments</p>
            <Button size="sm" variant="outline" onClick={() => setAddingInstr((v) => !v)}>
              <Plus className="size-3.5" /> Add
            </Button>
          </div>

          {/* Add instrument inline picker */}
          {addingInstr && (
            <div className="flex items-center gap-2 rounded-md bg-muted/40 p-3">
              <Select value={selectedInstrId} onValueChange={setSelectedInstrId}>
                <SelectTrigger className="flex-1">
                  <SelectValue placeholder="Select instrument…" />
                </SelectTrigger>
                <SelectContent>
                  {available.map((i) => (
                    <SelectItem key={i.id} value={String(i.id)}>
                      {i.name}
                      <span className="ml-2 text-xs text-muted-foreground">{i.asset_type}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                size="sm"
                onClick={() => addInstrMut.mutate()}
                disabled={!selectedInstrId || addInstrMut.isPending}
              >
                {addInstrMut.isPending ? '…' : 'Add'}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setAddingInstr(false);
                  setSelectedInstrId('');
                }}
              >
                Cancel
              </Button>
            </div>
          )}

          {(instruments as CategoryInstrument[]).length === 0 ? (
            <p className="text-sm text-muted-foreground">No instruments in this category yet.</p>
          ) : (
            <div className="overflow-hidden rounded-md border border-border">
              <table className="w-full text-sm">
                <thead className="bg-muted/40">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium text-muted-foreground">Name</th>
                    <th className="px-3 py-2 text-left font-medium text-muted-foreground">ISIN</th>
                    <th className="px-3 py-2 text-left font-medium text-muted-foreground">Type</th>
                    <th className="w-10" />
                  </tr>
                </thead>
                <tbody>
                  {(instruments as CategoryInstrument[]).map((instr) => (
                    <tr key={instr.instrument_id} className="border-t border-border">
                      <td className="px-3 py-2 font-medium">{instr.instrument_name}</td>
                      <td className="px-3 py-2 font-mono text-xs text-muted-foreground">
                        {instr.isin ?? '—'}
                      </td>
                      <td className="px-3 py-2">
                        <Badge variant="outline">{instr.asset_type}</Badge>
                      </td>
                      <td className="px-3 py-2 text-center">
                        <Button
                          size="icon"
                          variant="ghost"
                          className="size-7"
                          onClick={() => removeInstrMut.mutate(instr.instrument_id)}
                          disabled={removeInstrMut.isPending}
                        >
                          <X className="size-3.5 text-destructive" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// --- Page ---
function CategoriesPage() {
  const [showCreateForm, setShowCreateForm] = useState(false);
  const { data: categories = [], isLoading } = useQuery({
    queryKey: ['categories'],
    queryFn: categoriesApi.list,
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Categories</h1>
          <p className="text-sm text-muted-foreground">
            Group instruments into custom categories for allocation tracking.
          </p>
        </div>
        {!showCreateForm && (
          <Button onClick={() => setShowCreateForm(true)}>
            <Plus className="size-4" /> New Category
          </Button>
        )}
      </div>

      {showCreateForm && (
        <CategoryForm
          onCancel={() => setShowCreateForm(false)}
          onSuccess={() => setShowCreateForm(false)}
        />
      )}

      {isLoading ? (
        <div className="space-y-3">
          {[1, 2, 3].map((n) => (
            <div key={n} className="h-16 animate-pulse rounded-lg bg-muted" />
          ))}
        </div>
      ) : (categories as Category[]).length === 0 && !showCreateForm ? (
        <div className="flex flex-col items-center gap-4 rounded-xl border border-dashed border-border py-16 text-center">
          <Tags className="size-10 text-muted-foreground/50" />
          <div>
            <p className="font-medium">No categories yet</p>
            <p className="text-sm text-muted-foreground">
              Create categories to group instruments and track allocation targets.
            </p>
          </div>
          <Button onClick={() => setShowCreateForm(true)}>
            <Plus className="size-4" /> New Category
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          {(categories as Category[]).map((cat) => (
            <CategoryCard key={cat.id} category={cat} />
          ))}
        </div>
      )}
    </div>
  );
}
