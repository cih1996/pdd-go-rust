export {}

interface DesktopUpdateStatus {
  manifest_url: string
  app_version: string
  current_version: string
  current_source: 'bundled' | 'updated'
  pending_version?: string | null
  update_ready: boolean
  checking: boolean
  last_checked_at?: string | null
  last_error?: string | null
}

interface DesktopUpdateResult {
  ok: boolean
  available: boolean
  downloaded: boolean
  message: string
  status: DesktopUpdateStatus
}

declare global {
  interface Window {
    desktopApp?: {
      platform: string
      isElectron: boolean
      startService: (name: 'adapter' | 'opencv' | 'ocr') => Promise<{ ok: boolean; message: string }>
      getUpdateStatus: () => Promise<DesktopUpdateStatus>
      setUpdateManifestUrl: (manifestUrl: string) => Promise<DesktopUpdateStatus>
      checkForUpdates: () => Promise<DesktopUpdateResult>
      restartForUpdate: () => Promise<{ ok: boolean }>
      onUpdateStatus: (callback: (status: DesktopUpdateStatus) => void) => () => void
    }
  }
}
