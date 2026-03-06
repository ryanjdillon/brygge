import { useQuery } from '@tanstack/vue-query'

export interface WeatherData {
  windSpeed: number
  windDirection: string
  waveHeight: number
  temperature: number
  humidity: number
  updatedAt: string
}

async function fetchWeather(): Promise<WeatherData> {
  const response = await fetch('/api/v1/weather')
  if (!response.ok) {
    throw new Error('Failed to fetch weather data')
  }
  return response.json()
}

export function useWeather() {
  return useQuery({
    queryKey: ['weather'],
    queryFn: fetchWeather,
    refetchInterval: 5 * 60 * 1000,
    staleTime: 2 * 60 * 1000,
  })
}
