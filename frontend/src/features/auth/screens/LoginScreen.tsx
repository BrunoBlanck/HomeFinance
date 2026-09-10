import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { ApiError, NetworkError } from '@/api/client'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { Button } from '@/components/Button/Button'
import { PasswordField } from '@/components/PasswordField/PasswordField'
import { TextField } from '@/components/TextField/TextField'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { sessionQueryOptions } from '@/lib/session'
import { login } from '../api/auth'
import { AuthForm } from '../components/AuthForm'
import { AuthLayout } from '../components/AuthLayout'
import { loginSchema } from '../schemas/auth'
import { clearRegistration, readRegistrationTokenFor } from '../storage/registrationToken'

const OFFLINE = 'Não foi possível entrar agora. Verifique sua conexão e tente de novo.'

/** Uma única frase para conta inexistente e senha errada. Diferenciar as duas
 *  entregaria de graça a lista de quem tem conta aqui. */
function loginErrorMessage(error: unknown): string | undefined {
  if (!error) return undefined
  if (error instanceof NetworkError) return OFFLINE
  if (error instanceof ApiError) {
    if (error.status === 401) return 'E-mail ou senha incorretos.'
    // 403 EMAIL_NOT_VERIFIED não vira mensagem: a tela navega para a confirmação.
    if (error.status === 403 && error.code === 'EMAIL_NOT_VERIFIED') return undefined
    if (error.status >= 500) return OFFLINE
  }
  return messageForError(error)
}

export function LoginScreen() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const flash = useRouterState({ select: (state) => state.location.state.flash })

  const form = useForm({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: '', password: '' },
    mode: 'onSubmit',
    reValidateMode: 'onBlur',
    shouldFocusError: true,
  })

  const mutation = useMutation({
    mutationFn: login,
    onSuccess(session) {
      // Sessão criada: o fluxo de cadastro acabou, então a capacidade que
      // sobrou dele não tem mais razão para existir neste navegador.
      clearRegistration()
      queryClient.setQueryData(sessionQueryOptions.queryKey, session)
      navigate({ to: '/', replace: true })
    },
    onError(error, variables) {
      if (
        !(error instanceof ApiError) ||
        error.status !== 403 ||
        error.code !== 'EMAIL_NOT_VERIFIED'
      ) {
        return
      }

      // O 403 não devolve `registrationToken` — e não poderia: o e-mail é de
      // quem quer que seja o dono do endereço, e entregar a metade secreta do
      // cadastro a quem só sabe a senha reabriria o pre-hijacking pelo login.
      // Com o token DESTE endereço, o código que o backend acabou de reemitir
      // na MESMA tentativa já serve. Um token de outro endereço não conta: para
      // o servidor ele é indistinguível de token nenhum, e mandar a pessoa para
      // a confirmação com ele só produziria um beco — todo código falharia e o
      // caminho de volta ao cadastro ficaria escondido.
      if (readRegistrationTokenFor(variables.email) !== null) {
        navigate({
          to: '/confirmar-email',
          state: {
            email: variables.email,
            flash: {
              tone: 'info',
              // Verdadeira com e sem envio. O backend só reemite se houver
              // exatamente uma tentativa viva E a janela de envio permitir, então
              // prometer "enviamos um código novo agora" faria a pessoa esperar
              // um e-mail que pode não vir — e ignorar o código válido que já
              // está na caixa. Esta redação orienta a ação certa sem afirmar um
              // envio que talvez não tenha acontecido.
              message:
                'Confirme seu e-mail para entrar. Use o código que enviamos — se não encontrar, peça um novo.',
            },
          },
        })
        return
      }

      navigate({
        to: '/criar-conta',
        state: {
          email: variables.email,
          flash: {
            tone: 'info',
            title: 'Confirme seu e-mail para entrar.',
            message:
              'O código de 6 dígitos só vale no navegador em que o cadastro foi pedido. Preencha os dados de novo com o mesmo e-mail para receber um código novo.',
          },
        },
      })
    },
  })

  const onSubmit = form.handleSubmit((values) => {
    if (mutation.isPending) return
    mutation.mutate(values)
  })

  return (
    <AuthLayout
      documentTitle="Entrar"
      eyebrow="Acesso à conta"
      title="Entrar"
      intro="Use o e-mail e a senha da sua conta."
      notice={flash ? <FlashAlert flash={flash} /> : undefined}
      footer={
        <p>
          Ainda não tem conta? <TextLink to="/criar-conta">Criar conta</TextLink>
        </p>
      }
    >
      <AuthForm
        onSubmit={onSubmit}
        error={loginErrorMessage(mutation.error)}
        primary={
          <Button type="submit" variant="primary" fullWidth loading={mutation.isPending}>
            Entrar
          </Button>
        }
      >
        <TextField
          {...form.register('email')}
          label="E-mail"
          type="email"
          autoComplete="email"
          inputMode="email"
          autoCapitalize="off"
          spellCheck={false}
          error={form.formState.errors.email?.message}
        />
        <PasswordField
          {...form.register('password')}
          label="Senha"
          autoComplete="current-password"
          labelAction={<TextLink to="/esqueci-minha-senha">Esqueci minha senha</TextLink>}
          error={form.formState.errors.password?.message}
        />
      </AuthForm>
    </AuthLayout>
  )
}
