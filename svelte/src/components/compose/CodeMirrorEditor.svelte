<script lang="ts">
  // CodeMirror 6 wrapper for YAML/.env editing, styled like Dockge's editor
  // (dracula theme via thememirror, line numbers, tab handling, wrap).
  import { onMount, onDestroy } from 'svelte';
  import { EditorView, keymap, lineNumbers, placeholder as cmPlaceholder } from '@codemirror/view';
  import { EditorState } from '@codemirror/state';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { yaml } from '@codemirror/lang-yaml';
  import { dracula } from 'thememirror';

  interface Props {
    value?: string;
    disabled?: boolean;
    placeholder?: string;
    onchange?: (value: string) => void;
    onfocuschange?: (focused: boolean) => void;
  }

  let {
    value = '',
    disabled = false,
    placeholder = '',
    onchange,
    onfocuschange
  }: Props = $props();

  let host: HTMLDivElement;
  let view: EditorView | null = null;

  // Suppress feedback loops when we set the doc from outside.
  let applyingExternal = false;

  function buildExtensions(isDisabled: boolean) {
    const extensions = [
      dracula,
      yaml(),
      lineNumbers(),
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      EditorView.lineWrapping,
      EditorState.readOnly.of(isDisabled),
      EditorView.editable.of(!isDisabled),
      EditorView.updateListener.of((update) => {
        if (update.docChanged && !applyingExternal) {
          onchange?.(update.state.doc.toString());
        }
      }),
      EditorView.focusChangeEffect.of((_state, focusing) => {
        onfocuschange?.(focusing);
        return null;
      }),
      EditorView.theme({
        '&': {
          fontSize: '13px',
          fontFamily: "'JetBrains Mono', ui-monospace, monospace",
          borderRadius: '0.375rem'
        },
        '.cm-scroller': { overflow: 'auto' },
        '.cm-content': { padding: '8px 0' }
      })
    ];
    if (placeholder) extensions.push(cmPlaceholder(placeholder));
    return extensions;
  }

  function createEditor(doc: string, isDisabled: boolean) {
    view?.destroy();
    view = new EditorView({
      state: EditorState.create({ doc, extensions: buildExtensions(isDisabled) }),
      parent: host
    });
  }

  function setExternalValue(next: string) {
    if (!view) return;
    if (view.state.doc.toString() === next) return;
    applyingExternal = true;
    view.dispatch({
      changes: { from: 0, to: view.state.doc.length, insert: next }
    });
    applyingExternal = false;
  }

  onMount(() => {
    createEditor(value, disabled);
  });

  onDestroy(() => {
    view?.destroy();
    view = null;
  });

  // React to external value changes (e.g. GUI form → YAML regen).
  $effect(() => {
    setExternalValue(value);
  });

  // React to disabled toggles by rebuilding the editor (rare transition).
  let lastDisabled: boolean | undefined;
  $effect(() => {
    if (lastDisabled === undefined) {
      lastDisabled = disabled;
      return;
    }
    if (view && disabled !== lastDisabled) {
      lastDisabled = disabled;
      createEditor(view.state.doc.toString(), disabled);
    }
  });
</script>

<div bind:this={host} class="codemirror-host min-h-48 rounded-md border border-zinc-700 bg-[#282a36]"></div>
