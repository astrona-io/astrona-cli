<script setup lang="ts">
import { ref } from 'vue'

// A draggable, keyboard-adjustable divider between the task and the view.
// The value is the task panel's width as a percentage of the row.

const props = withDefaults(defineProps<{ min?: number; max?: number; controls: string }>(), {
  min: 20,
  max: 70,
})
const model = defineModel<number>({ required: true })
const emit = defineEmits<{ commit: [number] }>()

const el = ref<HTMLDivElement>()
const dragging = ref(false)

const clamp = (v: number) => Math.min(props.max, Math.max(props.min, Math.round(v * 10) / 10))

function fromPointer(ev: PointerEvent): void {
  const row = el.value?.parentElement?.getBoundingClientRect()
  if (!row || row.width === 0) return
  model.value = clamp(((ev.clientX - row.left) / row.width) * 100)
}

function down(ev: PointerEvent): void {
  if (ev.button !== 0) return
  dragging.value = true
  el.value?.setPointerCapture(ev.pointerId)
  ev.preventDefault()
}

function move(ev: PointerEvent): void {
  if (dragging.value) fromPointer(ev)
}

function up(ev: PointerEvent): void {
  if (!dragging.value) return
  dragging.value = false
  el.value?.releasePointerCapture(ev.pointerId)
  emit('commit', model.value)
}

function key(ev: KeyboardEvent): void {
  const step = ev.shiftKey ? 10 : 2
  let next: number | null = null
  if (ev.key === 'ArrowLeft' || ev.key === 'ArrowDown') next = model.value - step
  else if (ev.key === 'ArrowRight' || ev.key === 'ArrowUp') next = model.value + step
  else if (ev.key === 'Home') next = props.min
  else if (ev.key === 'End') next = props.max
  if (next === null) return
  ev.preventDefault()
  model.value = clamp(next)
  emit('commit', model.value)
}
</script>

<template>
  <div
    ref="el"
    class="divider"
    :class="{ 'divider--active': dragging }"
    role="separator"
    tabindex="0"
    aria-orientation="vertical"
    aria-label="Resize task and terminal"
    :aria-controls="controls"
    :aria-valuenow="Math.round(model)"
    :aria-valuemin="min"
    :aria-valuemax="max"
    @pointerdown="down"
    @pointermove="move"
    @pointerup="up"
    @pointercancel="up"
    @keydown="key"
  >
    <span class="divider__grip" aria-hidden="true" />
  </div>
</template>
