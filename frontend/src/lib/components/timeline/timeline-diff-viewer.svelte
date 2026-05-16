<script lang="ts">
    import { ArrowRight, FileDiff } from "@lucide/svelte";

    import type { DiffEntry, ItemDiffUi } from "$lib/components/timeline/types";
    import { buildDiff, truncateValue } from "$lib/components/timeline/utils";
    import type { HistoryItem } from "$lib/types/api";

    interface Props {
        item: HistoryItem;
        ui: ItemDiffUi;
    }

    let { item, ui }: Props = $props();

    const diffs = $derived<DiffEntry[]>(buildDiff(item));

    const filtered = $derived(
        diffs.filter((diff) => {
            if (!ui.showUnchanged && diff.status === "unchanged") return false;
            return true;
        }),
    );

    const prettyPath = (path: string) => {
        return path.replaceAll("_", " ");
    };
</script>

<div
    class="mt-2 ml-6 overflow-hidden rounded-md border border-slate-800 bg-slate-950/80 will-change-transform">
    <div
        class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-800 px-3 py-2">
        <div class="flex items-start gap-2 text-[10px]">
            <FileDiff class="mt-0.5 h-3.5 w-3.5 text-slate-300" />
            <div class="flex items-center gap-2">
                <span class="font-semibold tracking-wide text-slate-100 uppercase">
                    Diff Viewer
                </span>
                <span
                    class="text-[11px] leading-tight font-normal text-slate-500 normal-case">
                    Differences before and after sync.
                </span>
            </div>
        </div>
        <div class="flex items-center gap-2 text-[11px] text-slate-400">
            <label
                class="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-slate-700 bg-slate-900/60 px-2 py-1 select-none">
                <input
                    type="checkbox"
                    checked={ui.showUnchanged}
                    onchange={(event: Event) => {
                        const target = event.target as HTMLInputElement;
                        ui.showUnchanged = target.checked;
                    }}
                    class="h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-sky-500 focus:ring-0" />
                <span class="font-semibold text-slate-100">Unchanged</span>
            </label>
        </div>
    </div>
    <div class="space-y-3 p-3">
        {#if filtered.length}
            <ul class="divide-y divide-slate-800/60 text-[11px]">
                {#each filtered as diff (diff.path)}
                    <li class="group px-1 py-1.5">
                        <div class="flex flex-wrap items-start gap-2">
                            <span
                                class="max-w-full rounded bg-slate-800/80 px-1.5 py-0.5 font-mono text-[10px] break-all text-slate-300 group-hover:bg-slate-700/80">
                                {prettyPath(diff.path)}
                            </span>
                            <div class="flex min-w-40 flex-1 items-start gap-1.5">
                                <span
                                    class={`min-w-0 break-all ${diff.status === "removed" ? "text-red-400" : diff.status === "changed" ? "text-red-300" : "text-slate-500"}`}
                                    >{truncateValue(diff.before)}</span>
                                <ArrowRight
                                    class="mt-0.5 h-3.5 w-3.5 shrink-0 text-slate-600" />
                                <span
                                    class={`min-w-0 break-all ${diff.status === "added" ? "text-emerald-400" : diff.status === "changed" ? "text-emerald-300" : "text-slate-500"}`}
                                    >{truncateValue(diff.after)}</span>
                            </div>
                        </div>
                    </li>
                {/each}
            </ul>
        {:else}
            <p class="text-[11px] text-slate-500 italic">No differences.</p>
        {/if}
    </div>
</div>
