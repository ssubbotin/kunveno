import { describe, expect, it } from 'vitest';
import { Pace } from './pace';

describe('Pace', () => {
  it('segments every frame while recognition fits the frame time', () => {
    const p = new Pace(15);
    for (let i = 0; i < 30; i++) p.record(20);
    expect(p.every).toBe(1);
  });
  it('skips frames when recognition is slow, but never below once a second', () => {
    const p = new Pace(15);
    for (let i = 0; i < 30; i++) p.record(150); // over two frame times
    expect(p.every).toBe(3);
    for (let i = 0; i < 30; i++) p.record(5000);
    expect(p.every).toBe(15);
  });
  it('recovers when recognition gets fast again', () => {
    const p = new Pace(15);
    for (let i = 0; i < 30; i++) p.record(150);
    for (let i = 0; i < 60; i++) p.record(10);
    expect(p.every).toBe(1);
  });
  it('runs recognition on every n-th frame', () => {
    const p = new Pace(15);
    for (let i = 0; i < 30; i++) p.record(100); // every 2nd
    const runs = Array.from({ length: 6 }, () => p.shouldSegment());
    expect(runs.filter(Boolean)).toHaveLength(3);
  });
  it('measures the output frame rate over the last second', () => {
    const p = new Pace(15);
    for (let t = 0; t <= 1000; t += 100) p.frame(t);
    expect(p.fps()).toBe(10);
  });
});

describe('Pace and outliers', () => {
  it('is not thrown off by one very slow recognition', () => {
    const p = new Pace(15);
    for (let i = 0; i < 5; i++) p.record(17);
    p.record(10000); // the first recognition in a new worker can stall
    for (let i = 0; i < 3; i++) p.record(17);
    expect(p.every).toBe(1);
  });
});
