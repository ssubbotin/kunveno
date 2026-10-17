// meetingPath is the base of a meeting's item routes (notes, summary, recording parts): the room
// and the meeting, named by its start in milliseconds.
export function meetingPath(slug: string, meeting: number): string {
  return `/api/rooms/${encodeURIComponent(slug)}/meetings/${meeting}`;
}
