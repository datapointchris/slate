# The API's memory

The image sets two glibc tunables, `MALLOC_ARENA_MAX=2` and `MALLOC_MMAP_THRESHOLD_=131072`. Without
them the API's resident memory climbs in steps until the kernel kills it at its limit, though the
memory Python holds stays flat.

## glibc keeps what a request frees

The growth is freed memory that the allocator never hands back, not objects slate keeps.

- **Each request runs on a thread of its own.** A `def` handler runs on anyio's thread pool, which
  grows to 40 threads.
- **glibc gives threads their own arenas.** It allows up to eight per core, so 64 on an eight-core
  node.
- **An arena keeps what is freed in it.** Free space returns to the kernel only from an arena's
  top, so one live block above a freed region pins the region.
- **A large buffer lands in an arena after the first.** glibc maps a block of 128 KiB or more on
  its own and unmaps it on free. Each time it unmaps one, it raises that threshold to the block's
  size, up to 32 MiB. After the first multi-MiB list response, every later one is carved from an
  arena instead.

So resident memory becomes the sum of every arena's high-water mark. It rises each time a large
read lands on a thread whose arena has not yet held one that large, and it never falls. The largest
reads are `GET /issues?status=all`, `GET /initiatives/progress`, `GET /issues` and `GET /queue`.
Each answers several MiB of JSON and builds many times that in rows and models first.

**A view polling the queue reads `GET /queue/board`, not `GET /queue`.** The board answers the
same rows without their records. The readiness pass that both reads share sets the floor of what
it grows.

## Two arenas and a fixed mmap threshold hold it level

Each setting answers one half of the cause:

- **`MALLOC_ARENA_MAX=2`** makes threads share two arenas. Memory that one request frees is reused
  by the next, whatever thread it lands on.
- **`MALLOC_MMAP_THRESHOLD_=131072`** fixes the threshold at its default. Setting it turns off the
  dynamic raise, so every buffer of 128 KiB or more is mapped on its own and unmapped on free.

The two were chosen by replaying production's GET mix, weighted by a day of its request log, from
six clients at once against the release image under a 512 MiB cgroup limit:

| Allocator | Resident memory |
| --- | --- |
| glibc defaults | killed at 512 MiB after about 200 requests |
| `MALLOC_ARENA_MAX=2` alone | still climbing, 312 to 430 MiB, after 790 |
| both settings | level at 333 to 349 MiB from the first minute |
| jemalloc through `LD_PRELOAD` | level at 330 to 350 MiB, with one spike to 402 |

Those four shared one Postgres. The release image alone, under the same six clients, served about
2,500 requests in twelve minutes, roughly ten times production's average rate. It leveled at 385 to
395 MiB from the fourth minute, with a peak of 405. The level rises with how many large reads overlap,
because each one's rows and models are live until its response is sent.

*Rejected:* jemalloc. It levels off no lower, and needs a package in the image and a preload.

*Rejected:* a larger limit. The growth has no ceiling below the sum of 40 threads' peaks, so a
larger limit only moves the kill later.

*Rejected:* calling `malloc_trim` on a timer. It returns free pages, but leaves the arenas that
caused them in place.

## Reading it again

The request line carries `query` and `response_bytes`, so a step in the memory curve can be matched
to the reads that preceded it. Inside the process, glibc's `mallinfo2()` tells the two causes
apart. Its `uordblks` is memory in use and `fordblks` is memory freed and held. A climbing
`fordblks` under a flat `uordblks` is the allocator. Both climbing is something slate holds.
