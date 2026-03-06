import { useQuery } from '@tanstack/vue-query'

export interface WeatherData {
  temperature: number | null
  windSpeed: number | null
  windDirection: number | null
  humidity: number | null
  symbolCode: string
}

interface WeatherApiResponse {
  temperature: number | null
  wind_speed: number | null
  wind_direction: number | null
  humidity: number | null
  symbol_code: string
}

async function fetchWeather(): Promise<WeatherData> {
  const response = await fetch('/api/v1/weather')
  if (!response.ok) {
    throw new Error('Failed to fetch weather data')
  }
  const data: WeatherApiResponse = await response.json()
  return {
    temperature: data.temperature,
    windSpeed: data.wind_speed,
    windDirection: data.wind_direction,
    humidity: data.humidity,
    symbolCode: data.symbol_code,
  }
}

export function useWeather() {
  return useQuery({
    queryKey: ['weather'],
    queryFn: fetchWeather,
    refetchInterval: 5 * 60 * 1000,
    staleTime: 2 * 60 * 1000,
  })
}
