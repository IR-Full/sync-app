'use client'

import { create } from 'zustand'

import { readStorage, StorageKeys, writeStorage } from '@/shared/lib/storage'

export interface Settings {
  /** browser notifications for messages that arrive while the tab is hidden */
  desktopNotifications: boolean
  soundOnMessage: boolean
  /** when off, the client stops sending READ frames (others stop seeing your ticks) */
  sendReadReceipts: boolean
}

const DEFAULTS: Settings = {
  desktopNotifications: false,
  soundOnMessage: false,
  sendReadReceipts: true,
}

interface SettingsState extends Settings {
  set: <K extends keyof Settings>(key: K, value: Settings[K]) => void
  hydrate: () => void
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
  ...DEFAULTS,

  set: (key, value) => {
    set({ [key]: value } as Pick<Settings, typeof key>)
    const { desktopNotifications, soundOnMessage, sendReadReceipts } = get()
    writeStorage(StorageKeys.settings, {
      desktopNotifications,
      soundOnMessage,
      sendReadReceipts,
      [key]: value,
    })
  },

  hydrate: () =>
    set({ ...DEFAULTS, ...readStorage<Partial<Settings>>(StorageKeys.settings, {}) }),
}))
