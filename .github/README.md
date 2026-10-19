# Fixed: Delay profile saves, reorders and deletes no longer undo each other

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#8](https://github.com/chodeus/Sonarr/pull/8).

Delay profiles are ranked by `Order`: tagged profiles 1 to n, and the default profile (ID 1) last at `int.MaxValue`. Every change to that ranking read all the profiles and wrote them back:

- **Reorder** saved every column of every profile it had read, so a profile saved while it ran (the default profile's settings, say) was put back. It also never cleared the 30-second best-profile cache, so for up to 30 seconds after a drag, releases were held by the old ranking.
- **Saving a profile** wrote the order its edit form had loaded. If the profiles had been reordered since (in another tab, or by another client), the save moved it back, and two profiles could end up with the same order.
- **Delete** renumbered the rest the same way. If that renumber failed, the delete had already committed but the cache wasn't cleared, so the deleted profile could still hold releases for up to 30 seconds. The settings page also kept the old orders until a reload.
- **Add** gave a new profile the number of profiles as its order. When a tagged profile sat at the default's `int.MaxValue`, where an older reorder "after the default" left it, the new profile ranked above it.

## Changes

- `ProfileOrder` owns the numbering. Reorder and renumber give tagged profiles 1 to n and keep the default last. A move after the default now goes last instead of tying it, an unknown `afterId` still moves the profile first, and moving the default or a profile after itself changes nothing. A new profile gets the highest order plus one. If a tagged profile holds `int.MaxValue` or `int.MaxValue - 1`, where that would overflow or tie the default, or the default isn't last (an older save could store the order the client sent for it), the profiles are renumbered first.
- Reorder and Delete save only the `Order` column. Reorder clears the cache when it changes anything, and Add and Delete clear it in a `finally`, since a renumber or delete can commit before a later step fails.
- Update keeps the stored order: only Reorder changes it, and the default stays last.
- Add, Update, Delete and Reorder hold one lock (the service is a singleton), since each reads the orders and writes them back. A best-profile lookup that misses the cache reads and stores under the same lock, so a writer's cache clear can't land between the two. Cache hits, which RSS sync makes on every release comparison, don't take the lock.
- The settings page fetches the delay profiles again after a delete, so it shows the renumbered orders.

## Tests

- `DelayProfileServiceDbFixture` runs the real service over the real repository and SQLite database. The mock only forwards to the repository, or adds a second writer.
  - A default-profile save landing in the middle of a reorder's or a delete's write.
  - An update and a reorder from two requests at once, a delete and a reorder at once, and two adds at once.
  - An edit loaded before a reorder, and the default profile's order, including one an older save stored first, on Update and on Add.
  - The cache after a failed renumber, after a reorder, after an insert that fails once Add has renumbered, and when a lookup's read races a reorder or a delete. A cached lookup doesn't wait behind another lookup's read or a writer.
  - New profiles after a profile at `int.MaxValue` and after a gap, and dense orders after a reorder and a delete.
- `ProfileOrderFixture` covers moves up, down, first, last, after an unknown profile, and moves that change nothing, plus renumbering, the next order and making room.
- The existing `DelayProfileServiceFixture` reorder tests pass unchanged.
- Against the unmodified service, 17 of the new database tests fail. Each guard was then broken on purpose (19 mutations: each lock, including the lookup's, a lookup hit taking the lock, each column-only write, each cache clear, the stored order, the default's order, the next order and making room, and four in `ProfileOrder`), and its tests failed every time.
- The settings page change is checked by lint and the type check only. The frontend has no unit tests.
