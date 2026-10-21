// Pointer capture keeps a drag's events on the element (some test DOMs lack it).
// Owner: P1-W09.
export function capture(el: Element | null, pointerId: number): void {
  try {
    el?.setPointerCapture(pointerId);
  } catch {
    // no pointer capture: the drag still follows moves over the element
  }
}
