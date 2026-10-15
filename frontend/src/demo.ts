import type { Drive, Entry } from './library';
const samples = [
  ['2026-10-12--17-42-08', 24],
  ['2026-10-12--08-16-32', 18],
  ['2026-10-11--16-05-11', 42],
  ['2026-10-11--10-28-45', 12],
  ['2026-10-10--18-34-20', 31],
  ['2026-10-09--09-12-06', 16],
] as const;
export const demoDrives: Drive[] = samples.map(([id, count]) => ({
  id,
  date: new Date(
    id.replace('--', 'T').replace(/T(\d+)-(\d+)-(\d+)/, 'T$1:$2:$3'),
  ).getTime(),
  dated: true,
  segments: Array.from({ length: count }, (_, index) => ({
    index,
    url: `/sample/${id}/${index}/`,
  })),
}));
export const demoFiles: Entry[] = [
  { name: 'sample-road.mp4', url: '/sample-road.mp4', size: 0, modified: 0 },
];
