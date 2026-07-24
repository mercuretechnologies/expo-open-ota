import React, { useState } from 'react'
import { View, Text, ScrollView, TouchableOpacity } from 'react-native'
import JSONTree from 'react-native-json-tree'
import * as Clipboard from 'expo-clipboard'
import * as Updates from 'expo-updates'

export function UpdatesLogViewer({
  logs,
}: {
  logs: Updates.UpdatesLogEntry[]
}) {
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null)

  const handleCopy = async (log: Updates.UpdatesLogEntry, index: number) => {
    await Clipboard.setStringAsync(JSON.stringify(log, null, 2))
    setCopiedIndex(index)
    setTimeout(() => setCopiedIndex(null), 1500)
  }

  const formatDate = (timestamp: number) =>
    new Date(timestamp).toLocaleString('fr-FR', {
      hour12: false,
      timeZone: 'Europe/Paris',
    })

  return (
    <ScrollView contentContainerStyle={{ padding: 16 }}>
      {logs.map((log, index) => (
        <View
          key={index}
          style={{
            marginBottom: 16,
            padding: 12,
            backgroundColor: '#f3f4f6',
            borderRadius: 8,
          }}
        >
          <Text style={{ marginBottom: 4, fontWeight: 'bold' }}>
            {formatDate(log.timestamp)} - {log.level.toUpperCase()} - {log.code}
          </Text>

          {/* 👇 Fix horizontal overflow */}
          <ScrollView horizontal style={{ marginBottom: 8 }}>
            <JSONTree data={log} hideRoot={true} />
          </ScrollView>

          <TouchableOpacity
            onPress={() => handleCopy(log, index)}
            style={{
              alignSelf: 'flex-start',
              backgroundColor: '#2563eb',
              paddingVertical: 6,
              paddingHorizontal: 12,
              borderRadius: 4,
            }}
          >
            <Text style={{ color: '#fff' }}>
              {copiedIndex === index ? 'Copied!' : 'Copy JSON'}
            </Text>
          </TouchableOpacity>
        </View>
      ))}
    </ScrollView>
  )
}
