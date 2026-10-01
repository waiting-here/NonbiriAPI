import { operationKey } from '@shared/operations/api';
import { publishVersion, saveLevel, validateLevelOnServer, type LevelInput, type LevelRecord, type VersionRecord } from './api';

/** Each confirmed step is retained independently, including its original request key. */
export class PlaytestDraft {
  readonly input: LevelInput;
  readonly validateKey = operationKey();
  readonly saveKey = operationKey();
  readonly versionKey = operationKey();
  private validated = false;
  private saved: LevelRecord | null = null;
  private version: VersionRecord | null = null;
  constructor(readonly id: string | null, input: LevelInput) { this.input = structuredClone(input); }
  async run(onSaved: (record: LevelRecord, input: LevelInput) => void, assertCurrent: () => void): Promise<VersionRecord> {
    assertCurrent();
    if (!this.validated) { await validateLevelOnServer(this.input.draft, this.validateKey); assertCurrent(); this.validated = true; }
    if (!this.saved) { this.saved = await saveLevel(this.id, this.input, this.saveKey); assertCurrent(); onSaved(this.saved, this.input); }
    if (!this.version) this.version = await publishVersion(this.saved.id, this.saved.revision, this.versionKey);
    assertCurrent();
    return this.version;
  }
}
